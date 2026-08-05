package analyzer

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Shadow is one declared route that takes URLs away from the site
// mounted beside it, with the files it takes.
type Shadow struct {
	// Service is the deployable both the route and the site belong to.
	Service string
	// Route is the declared pattern, method and all.
	Route string
	// Handler is the handler var the shadowed request reaches instead
	// of the site.
	Handler string
	// Pos is the declaration position, path:line:col — the fix is at
	// the declaration, so that is where the warning points.
	Pos string
	// Files are the shadowed URL paths, sorted, as a browser asks for
	// them.
	Files []string
	// Segments is the route's path depth, which is what a file has to
	// clear to escape it.
	Segments int
}

// ShadowedSiteFiles reports the files a service's site would serve if a
// declared route were not in front of them.
//
// A Page or API pattern beats the site's "/" for every URL it matches,
// so a two-segment wildcard page takes every two-segment URL — the
// stylesheet at /assets/site.css included, which then reaches a handler
// that has never heard of it and answers an honest 404. Nothing fails,
// nothing logs, and the site deploys unstyled.
//
// The check does not reason about depth. It builds the router codegen
// builds — every declared pattern, plus "/" for the site — and asks it
// which pattern claims each file, so the answer comes from the same
// resolver that will serve production rather than from a rule about
// segment counts that could drift from it.
//
// Shadowing is legitimate: an app is free to own a URL space its site
// has no files in, and a media route at /media/{key} is doing exactly
// that on purpose. So this is a warning's input, never an error's.
//
// moduleDir is the directory the graph was analyzed from; site
// directories are recorded module-relative. Sites whose files are not
// on disk yet — a WithBuild output before its generator has run —
// report nothing, so callers should invoke this after the site build.
func ShadowedSiteFiles(moduleDir string, g *AppGraph) []Shadow {
	var out []Shadow
	for _, svc := range g.Services {
		site := siteOfService(g, svc)
		if site == nil {
			continue
		}
		routed := routedHandlers(g, svc)
		if len(routed) == 0 {
			continue
		}
		mux, ok := siteMux(routed)
		if !ok {
			// The app's own patterns cannot be registered together.
			// That is a startup panic with its own message; reporting
			// shadows on a router that cannot exist would only bury it.
			continue
		}
		byRoute := map[string][]string{}
		for _, f := range serveableFiles(moduleDir, site) {
			pattern := matchedPattern(mux, f)
			if pattern == "" || pattern == "/" {
				continue
			}
			byRoute[pattern] = append(byRoute[pattern], f)
		}
		for _, h := range routed {
			files := byRoute[muxPatternOf(h.Route)]
			if len(files) == 0 {
				continue
			}
			sort.Strings(files)
			out = append(out, Shadow{
				Service:  svc.Name,
				Route:    h.Route,
				Handler:  ShortVar(h),
				Pos:      h.Pos,
				Files:    files,
				Segments: routeSegments(h.Route),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Route < out[j].Route
	})
	return out
}

// Warning is the whole line a caller prints. The text lives here rather
// than at the two call sites so tako dev and tako deploy cannot drift
// into describing the same problem two ways.
func (s Shadow) Warning() string {
	var b strings.Builder
	if s.Pos != "" {
		b.WriteString(s.Pos + ": ")
	}
	fmt.Fprintf(&b, "⚠ %s shadows %d %s the site serves — %s %s %s instead of the site, and %s 404; ",
		s.Route,
		len(s.Files),
		pick(len(s.Files), "file", "files"),
		listFiles(s.Files),
		pick(len(s.Files), "reaches", "reach"),
		s.Handler,
		pick(len(s.Files), "answers", "answer"))
	fmt.Fprintf(&b, "move %s deeper than the route's %d segments, or narrow the pattern (tako/site-shadow)",
		pick(len(s.Files), "it", "them"), s.Segments)
	return b.String()
}

func pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// listFiles names at most three. A route over a generated site can
// shadow hundreds of files, and a warning that prints them all is one
// nobody reads to the end.
func listFiles(files []string) string {
	if len(files) <= 3 {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(files[:3], ", "), len(files)-3)
}

// siteOfService is codegen's siteOf: the site declared in the service's
// own package is the one it serves.
func siteOfService(g *AppGraph, svc Service) *Resource {
	for i, r := range g.Resources {
		if r.Kind == "site" && r.Package == svc.Package {
			return &g.Resources[i]
		}
	}
	return nil
}

// routedHandlers are the service's handlers that hold a mux pattern.
func routedHandlers(g *AppGraph, svc Service) []Handler {
	var out []Handler
	for _, h := range g.Handlers {
		if h.Service != svc.Name || h.Route == "" {
			continue
		}
		out = append(out, h)
	}
	return out
}

// siteMux mirrors the generated main: declared patterns, plus the site
// at "/". ok is false when the patterns cannot coexist.
func siteMux(routed []Handler) (mux *http.ServeMux, ok bool) {
	defer func() {
		if recover() != nil {
			mux, ok = nil, false
		}
	}()
	mux = http.NewServeMux()
	mux.Handle("/", http.NotFoundHandler())
	for _, h := range routed {
		mux.Handle(muxPatternOf(h.Route), http.NotFoundHandler())
	}
	return mux, true
}

// muxPatternOf is codegen's muxPattern: a bare "/" route is anchored so
// it does not become a second catch-all.
func muxPatternOf(route string) string {
	if strings.HasSuffix(route, " /") {
		return route + "{$}"
	}
	return route
}

// matchedPattern asks the mux which pattern claims a GET of urlPath.
func matchedPattern(mux *http.ServeMux, urlPath string) string {
	r := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: urlPath},
		Host:   "tako.invalid",
	}
	_, pattern := mux.Handler(r)
	return pattern
}

// serveableFiles are the URL paths of the files the site handler would
// serve, from the directory it actually serves: a built site's output
// when it has one, the declared directory otherwise.
func serveableFiles(moduleDir string, site *Resource) []string {
	src := site.Dir
	if site.BuildOut != "" {
		src = site.BuildOut
	}
	root := filepath.Join(moduleDir, filepath.FromSlash(src))
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			// templates/ backs site.Render and is never served
			// statically, so nothing in it can be shadowed.
			if rel == "templates" {
				return filepath.SkipDir
			}
			return nil
		}
		out = append(out, servedURL(rel))
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil
	}
	return out
}

// servedURL is the URL a browser asks for to get this file, which is
// not always the file's own path: the site handler resolves pretty URLs
// (/about → about/index.html), so an index.html is reachable one
// segment above where it sits. Asking about the literal path would
// report /about/index.html as shadowed while /about serves perfectly —
// a warning about a page nobody is missing, which is the kind that
// teaches people to skip the real ones.
func servedURL(rel string) string {
	if rel == "index.html" {
		return "/"
	}
	if strings.HasSuffix(rel, "/index.html") {
		return path.Join("/", strings.TrimSuffix(rel, "/index.html"))
	}
	return path.Join("/", rel)
}

// routeSegments counts the path segments a route pattern matches — the
// depth a file has to clear to get out from under it.
func routeSegments(route string) int {
	p := route
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = p[i+1:]
	}
	p = strings.Trim(p, "/")
	if p == "" {
		return 0
	}
	return strings.Count(p, "/") + 1
}
