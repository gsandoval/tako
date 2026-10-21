package analyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// IsCfireProject reports whether dir is a cfire project (has .cf files outside .tako and no go.mod).
func IsCfireProject(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return false
	}
	var hasCf bool
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".tako" || base == ".cfire" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".cf") && !strings.HasSuffix(path, "_test.cf") {
			hasCf = true
		}
		return nil
	})
	return hasCf
}

func findCfireBin() string {
	if env := os.Getenv("CFIRE_BIN"); env != "" {
		return env
	}
	if path, err := exec.LookPath("cfire"); err == nil {
		return path
	}
	local := "/Users/memo/Programming/cfire/cfire"
	if _, err := os.Stat(local); err == nil {
		return local
	}
	return "cfire"
}

// Cfire outline JSON structures emitted by `cfire outline --json <dir>`
type cfireOutlineOutput struct {
	Files map[string]cfireOutlineFile `json:"files"`
}

type cfireOutlineFile struct {
	Package    string               `json:"package"`
	Imports    []string             `json:"imports"`
	Types      []cfireOutlineType   `json:"types"`
	GlobalVars []cfireOutlineGlobal `json:"global_vars"`
	Functions  []cfireOutlineFunc   `json:"functions"`
}

type cfireOutlineType struct {
	Name   string              `json:"name"`
	Kind   string              `json:"kind"`
	Fields []cfireOutlineField `json:"fields"`
}

type cfireOutlineField struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

type cfireOutlineGlobal struct {
	Name    string   `json:"name"`
	Line    int      `json:"line"`
	Call    string   `json:"call"`
	TypeArg string   `json:"type_arg"`
	Args    []string `json:"args"`
}

type cfireOutlineFunc struct {
	Name    string   `json:"name"`
	Params  []string `json:"params"`
	Returns string   `json:"returns"`
	Async   bool     `json:"async"`
}

var pathParamRegex = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// analyzeCfire runs cfire outline --json and builds an AppGraph or ErrorList.
func analyzeCfire(cfg Config) (*TypedGraph, error) {
	cfireBin := findCfireBin()
	cmd := exec.Command(cfireBin, "outline", "--json", cfg.Dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cfire outline --json failed: %w: %s", err, stderr.String())
	}

	var outline cfireOutlineOutput
	if err := json.Unmarshal(stdout.Bytes(), &outline); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cfire outline JSON: %w\noutput was: %s", err, stdout.String())
	}

	var errs ErrorList
	graph := &AppGraph{
		Module:    filepath.Base(cfg.Dir),
		Services:  []Service{},
		Resources: []Resource{},
		Handlers:  []Handler{},
		Edges:     []Edge{},
	}

	// Index all types and functions across all files
	typesByName := make(map[string]cfireOutlineType)
	funcsByName := make(map[string]cfireOutlineFunc)

	// Sort file paths for deterministic processing
	var filePaths []string
	for p := range outline.Files {
		filePaths = append(filePaths, p)
	}
	sort.Strings(filePaths)

	for _, p := range filePaths {
		f := outline.Files[p]
		for _, t := range f.Types {
			typesByName[t.Name] = t
		}
		for _, fn := range f.Functions {
			funcsByName[fn.Name] = fn
		}
	}

	// Collect service handlers
	serviceHandlerMap := make(map[string][]string) // service name -> handler var names
	var workerHandlers []Handler

	for _, p := range filePaths {
		f := outline.Files[p]
		pkgName := f.Package
		if pkgName == "" {
			pkgName = "main"
		}
		defaultSvc := pkgName

		for _, gv := range f.GlobalVars {
			pos := fmt.Sprintf("%s:%d:1", p, gv.Line)
			if !strings.HasPrefix(gv.Call, "tako.") {
				continue
			}
			ctor := strings.TrimPrefix(gv.Call, "tako.")

			switch ctor {
			case "entity":
				resName := ""
				if len(gv.Args) > 0 {
					resName = gv.Args[0]
				}
				if resName == "" {
					errs = append(errs, &Error{
						Pos:  pos,
						Msg:  "entity name must be a non-empty string literal",
						Rule: "const-name",
					})
				}

				entityType, exists := typesByName[gv.TypeArg]
				var indexes []string
				if exists {
					var idFields []cfireOutlineField
					for _, fld := range entityType.Fields {
						tag := reflect.StructTag(fld.Tag)
						etag := tag.Get("entity")
						if etag == "id" {
							idFields = append(idFields, fld)
						} else if etag == "index" {
							indexes = append(indexes, fld.Name)
						}
					}

					if len(idFields) == 0 {
						errs = append(errs, &Error{
							Pos:  pos,
							Msg:  fmt.Sprintf("entity %q type %s must have exactly one field tagged `entity:\"id\"`", resName, gv.TypeArg),
							Rule: "entity-id",
						})
					} else if len(idFields) > 1 {
						errs = append(errs, &Error{
							Pos:  pos,
							Msg:  fmt.Sprintf("entity %q type %s has multiple fields tagged `entity:\"id\"`", resName, gv.TypeArg),
							Rule: "entity-id",
						})
					} else if idFields[0].Type != "str" && idFields[0].Type != "string" {
						errs = append(errs, &Error{
							Pos:  pos,
							Msg:  fmt.Sprintf("entity %q id field %s must have type str (got %s)", resName, idFields[0].Name, idFields[0].Type),
							Rule: "entity-id",
						})
					}
				}

				graph.Resources = append(graph.Resources, Resource{
					Kind:    "entity",
					Name:    resName,
					Var:     gv.Name,
					Package: pkgName,
					Type:    gv.TypeArg,
					Indexes: indexes,
					Pos:     pos,
				})

			case "bucket", "queue", "topic", "cache", "lock", "secret", "counter", "histogram", "site":
				resName := ""
				if len(gv.Args) > 0 {
					resName = gv.Args[0]
				}
				if resName == "" {
					errs = append(errs, &Error{
						Pos:  pos,
						Msg:  fmt.Sprintf("%s name must be a non-empty string literal", ctor),
						Rule: "const-name",
					})
				}
				res := Resource{
					Kind:    ctor,
					Name:    resName,
					Var:     gv.Name,
					Package: pkgName,
					Type:    gv.TypeArg,
					Pos:     pos,
				}
				if ctor == "site" {
					res.Dir = resName
				}
				graph.Resources = append(graph.Resources, res)

			case "api", "page":
				route := ""
				handlerName := ""
				if len(gv.Args) > 0 {
					route = gv.Args[0]
				}
				if len(gv.Args) > 1 {
					handlerName = gv.Args[1]
				}

				if route == "" {
					errs = append(errs, &Error{
						Pos:  pos,
						Msg:  fmt.Sprintf("tako.%s route must be a non-empty string literal", ctor),
						Rule: "const-name",
					})
				}

				var reqType, resType string
				var bindings []Binding
				fn, fnFound := funcsByName[handlerName]
				if fnFound {
					resType = fn.Returns
					// Extract req type from params: "ctx: tako::Ctx", "req: GreetReq"
					for _, param := range fn.Params {
						parts := strings.SplitN(param, ":", 2)
						if len(parts) == 2 {
							pName := strings.TrimSpace(parts[0])
							pType := strings.TrimSpace(parts[1])
							if pName == "req" || (!strings.Contains(pType, "Ctx") && !strings.Contains(pType, "ctx")) {
								reqType = pType
							}
						}
					}
				}

				// Inspect path and query bindings from ReqType
				if reqType != "" {
					if st, ok := typesByName[reqType]; ok {
						routeParamMatches := pathParamRegex.FindAllStringSubmatch(route, -1)
						routeParams := make(map[string]bool)
						for _, m := range routeParamMatches {
							if len(m) > 1 {
								routeParams[m[1]] = true
							}
						}

						pathFields := make(map[string]bool)
						for _, fld := range st.Fields {
							tag := reflect.StructTag(fld.Tag)
							if p := tag.Get("path"); p != "" {
								pathFields[p] = true
								bindings = append(bindings, Binding{
									Field: fld.Name,
									Kind:  "path",
									Name:  p,
									Type:  fld.Type,
								})
								if !routeParams[p] {
									errs = append(errs, &Error{
										Pos:  pos,
										Msg:  fmt.Sprintf("request struct %s specifies path %q not found in route %q", reqType, p, route),
										Rule: "route-params",
									})
								}
							} else if q := tag.Get("query"); q != "" {
								bindings = append(bindings, Binding{
									Field: fld.Name,
									Kind:  "query",
									Name:  q,
									Type:  fld.Type,
								})
							} else if h := tag.Get("header"); h != "" {
								bindings = append(bindings, Binding{
									Field: fld.Name,
									Kind:  "header",
									Name:  h,
									Type:  fld.Type,
								})
							}
						}

						for rp := range routeParams {
							if !pathFields[rp] {
								errs = append(errs, &Error{
									Pos:  pos,
									Msg:  fmt.Sprintf("route parameter %q has no matching field in request struct %s", rp, reqType),
									Rule: "route-params",
								})
							}
						}
					}
				}

				h := Handler{
					Kind:     ctor,
					Var:      gv.Name,
					Package:  pkgName,
					Service:  defaultSvc,
					Route:    route,
					ReqType:  reqType,
					ReqRef:   reqType,
					ResRef:   resType,
					Bindings: bindings,
					Pos:      pos,
				}
				graph.Handlers = append(graph.Handlers, h)
				serviceHandlerMap[defaultSvc] = append(serviceHandlerMap[defaultSvc], gv.Name)

			case "worker":
				queueBinding := ""
				if len(gv.Args) > 0 {
					queueBinding = gv.Args[0]
				}
				svcName := strings.ToLower(gv.Name)
				h := Handler{
					Kind:    "worker",
					Var:     gv.Name,
					Package: pkgName,
					Service: svcName,
					Binding: queueBinding,
					Pos:     pos,
				}
				workerHandlers = append(workerHandlers, h)
				graph.Handlers = append(graph.Handlers, h)

			case "subscribe":
				topicBinding := ""
				if len(gv.Args) > 0 {
					topicBinding = gv.Args[0]
				}
				svcName := strings.ToLower(gv.Name)
				h := Handler{
					Kind:        "subscribe",
					Var:         gv.Name,
					Package:     pkgName,
					Service:     svcName,
					Binding:     topicBinding,
					BindingKind: "topic",
					Pos:         pos,
				}
				graph.Handlers = append(graph.Handlers, h)
				serviceHandlerMap[svcName] = append(serviceHandlerMap[svcName], gv.Name)

			case "cron":
				schedule := ""
				if len(gv.Args) > 0 {
					schedule = gv.Args[0]
				}
				h := Handler{
					Kind:     "cron",
					Var:      gv.Name,
					Package:  pkgName,
					Service:  defaultSvc,
					Schedule: schedule,
					Pos:      pos,
				}
				graph.Handlers = append(graph.Handlers, h)
				serviceHandlerMap[defaultSvc] = append(serviceHandlerMap[defaultSvc], gv.Name)
			}
		}
	}

	if len(errs) > 0 {
		return nil, errs
	}

	// Build default services
	var svcNames []string
	for name := range serviceHandlerMap {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)

	for _, name := range svcNames {
		graph.Services = append(graph.Services, Service{
			Name:     name,
			Package:  name,
			Handlers: serviceHandlerMap[name],
			Profile:  "default",
		})
	}

	// Build worker services
	for _, wh := range workerHandlers {
		graph.Services = append(graph.Services, Service{
			Name:     wh.Service,
			Package:  wh.Package,
			Handlers: []string{wh.Var},
			Profile:  "batch",
		})
	}

	return &TypedGraph{
		Graph: graph,
		Dir:   cfg.Dir,
	}, nil
}
