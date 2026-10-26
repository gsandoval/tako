package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"regexp"
	"strings"
	"sync/atomic"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
)

// siteSet is the whole parsed template registry as ONE value: the
// templates, the per-template parse errors, whether the set has a shell,
// and the setup failure that makes the whole thing unrenderable.
//
// One value because they are read together and must never be mixed
// (FL23). renderNamed used to read siteParseErrors and then
// siteTemplates from two package vars, so a set arriving between those
// two lines could answer "template not found" for a template that had
// just been installed, or report a parse error belonging to the set
// before it.
//
// A siteSet is IMMUTABLE once published. Every field is built into
// locals and the whole thing is stored in one atomic write, so a reader
// either sees the old set entirely or the new one entirely.
type siteSet struct {
	templates map[string]*template.Template
	parseErrs map[string]error
	setupErr  error
	hasLayout bool
}

// currentSet is the published set. Loaded once per render, which is what
// lets an in-flight render finish against the set it started with even
// as a newer one is published under it.
var currentSet atomic.Pointer[siteSet]

// RegisterSiteTemplates parses <site>/templates for site.Render:
// layout.html is the shell, every other *.html fills its "content"
// block. Generated mains call this with the embedded site FS. Parse
// errors are recorded per template and surface at render time with the
// template named — the dev loop iterates on exactly that text.
func RegisterSiteTemplates(fsys fs.FS, opts SiteOptions) {
	fallbackNotFound = opts.NotFound
	hooks.RenderSite = renderSite
	hooks.RenderFragment = renderFragment
	hooks.PrepareTemplates = prepareTemplates
	hooks.UseTemplates = useTemplates
	hooks.RenderTemplateSet = renderTemplateSet
	hooks.RenderTemplateSetFragment = renderTemplateSetFragment
	siteTrustedHTML = opts.TrustedHTML
	hooks.RenderMarkdown = func(src string) ([]byte, error) {
		return RenderMarkdown(src, siteTrustedHTML)
	}
	// Boot is prepare-then-use, so there is ONE parser and the set a
	// deployed service starts with is built exactly the way a replacement
	// is. A boot-time failure publishes the set carrying it, because
	// "the site has no templates/" has to reach a render as an error and
	// not as an empty registry that answers "not found" for everything.
	currentSet.Store(parseSiteTemplates(fsys))
}

// parseSiteTemplates builds a complete set from fsys. It touches no
// package state: everything lands in locals and the caller decides
// whether the result is ever published (FL23). That is what makes a
// candidate set possible — and it is also why a half-built registry can
// no longer be observed, which a mutex alone would not have fixed.
func parseSiteTemplates(fsys fs.FS) *siteSet {
	set := &siteSet{
		templates: map[string]*template.Template{},
		parseErrs: map[string]error{},
	}
	if _, err := fs.ReadDir(fsys, "templates"); err != nil {
		// Not "web/templates/…": the site directory is whatever tako.Site
		// was given, and naming a path this app may not have sends the
		// reader looking for the wrong thing.
		set.setupErr = errors.New("the site has no templates/ directory — site.Render needs templates/layout.html inside the declared site directory, plus one file per page")
		return set
	}
	// layout.html is OPTIONAL: a site whose shell comes from a static
	// generator (Hugo, etc.) renders only FRAGMENTS — site.Render is
	// what needs the shell, and says so at call time.
	layout := template.New("layout.html").Funcs(siteFuncs())
	if _, statErr := fs.Stat(fsys, "templates/layout.html"); statErr == nil {
		if _, layoutErr := layout.ParseFS(fsys, "templates/layout.html"); layoutErr != nil {
			set.setupErr = fmt.Errorf("site templates: %v (layout.html is the shell every page renders inside)", layoutErr)
			return set
		}
		set.hasLayout = true
	}

	// Two tiers: PAGES define the layout's "content" block (each gets
	// its own clone — their block names would collide in one set);
	// PARTIALS don't, and parse into the shared base so any page or
	// fragment can {{ template }} them (post cards, toggles).
	//
	// The set is a TREE, walked rather than listed, and every template is
	// keyed by its path RELATIVE to templates/ — so an app can put the
	// admin's templates and the public site's in separate directories, and
	// a site bundle can own the templates it renders with.
	//
	// Each is parsed BY NAME rather than with ParseFS, and that is the whole
	// reason a WalkDir alone would not have been enough: ParseFS names a
	// template after its base, so admin/nav.html and site/nav.html would be
	// one template and the second would silently win.
	//
	// A top-level template's path IS its base name, so every flat site keeps
	// the names it had.
	type sourced struct{ name, src string }
	var pages, partials []sourced
	walkErr := fs.WalkDir(fsys, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := strings.TrimPrefix(p, "templates/")
		// Only the TOP-LEVEL layout.html is the shell. One deeper in the
		// tree is an ordinary template with an ordinary name, so a bundle
		// can carry its own without becoming the root for everybody.
		if name == "layout.html" || !strings.HasSuffix(name, ".html") {
			return nil
		}
		raw, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			set.parseErrs[name] = readErr
			return nil
		}
		if definesContent(string(raw)) {
			pages = append(pages, sourced{name, string(raw)})
		} else {
			partials = append(partials, sourced{name, string(raw)})
		}
		return nil
	})
	if walkErr != nil {
		set.setupErr = fmt.Errorf("site templates: %v", walkErr)
		return set
	}

	for _, t := range partials {
		if _, err := layout.New(t.name).Parse(t.src); err != nil {
			set.parseErrs[t.name] = err
		}
	}
	for _, t := range partials {
		if set.parseErrs[t.name] == nil {
			set.templates[t.name] = layout // fragments render from the shared base
		}
	}
	for _, p := range pages {
		t, err := layout.Clone()
		if err == nil {
			_, err = t.New(p.name).Parse(p.src)
		}
		if err != nil {
			set.parseErrs[p.name] = err
			continue
		}
		set.templates[p.name] = t
	}
	return set
}

var definesContentRe = regexp.MustCompile(`\{\{-?\s*(define|block)\s+"content"`)

func definesContent(src string) bool { return definesContentRe.MatchString(src) }

// siteTrustedHTML is the site's declared markdown posture
// (WithTrustedHTML): sanitized by default, raw when opted out. It is
// declaration-time configuration rather than part of the set — swapping
// templates never changes a site's markdown posture.
var siteTrustedHTML bool

func renderSite(ctx context.Context, name string, data any) ([]byte, error) {
	return renderIn(currentSet.Load(), ctx, name, true, data)
}

// renderFragment executes the named template itself — no layout, the
// htmx partial path. Same template set, same funcs, same escaping.
func renderFragment(ctx context.Context, name string, data any) ([]byte, error) {
	return renderIn(currentSet.Load(), ctx, name, false, data)
}

// renderIn renders from ONE set, handed in. Every caller loads the
// published pointer exactly once (or brings a candidate), so a render
// finishes against the set it started with (FL23).
func renderIn(set *siteSet, ctx context.Context, name string, withLayout bool, data any) ([]byte, error) {
	if set == nil {
		return nil, errors.New("tako: site templates are not registered — a generated service registers them at boot")
	}
	if withLayout && set.setupErr == nil && !set.hasLayout {
		return nil, errors.New("site.Render needs templates/layout.html (the shell every page renders inside) — a site whose shell is generated (Hugo, etc.) renders fragments via site.Fragment instead")
	}
	root := name
	if withLayout {
		root = "layout.html"
	}
	return renderNamed(set, ctx, name, root, data)
}

func renderNamed(set *siteSet, ctx context.Context, name, root string, data any) ([]byte, error) {
	if set.setupErr != nil {
		return nil, set.setupErr
	}
	if err, ok := set.parseErrs[name]; ok {
		return nil, fmt.Errorf("template %s: %v", name, err)
	}
	t, ok := set.templates[name]
	if !ok {
		return nil, fmt.Errorf("template %s not found under web/templates/ (templates parse at startup; the layout is layout.html)", name)
	}
	// Clone per render: Funcs mutates the set, and requests race.
	t, err := t.Clone()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	// The token travels the context so {{ csrf }} works anywhere in the
	// tree without threading it through handler data.
	if err := t.Funcs(requestFuncs(ctx)).ExecuteTemplate(&buf, root, data); err != nil {
		return nil, fmt.Errorf("template %s: %v", name, err)
	}
	return buf.Bytes(), nil
}

// siteFuncs are the parse-time function stubs; requestFuncs rebinds the
// request-scoped ones at execute time.
func siteFuncs() template.FuncMap {
	return template.FuncMap{
		"csrf":      func() template.HTML { return "" },
		"csrfToken": func() string { return "" },
		"markdown":  renderMarkdownFunc,
		"user":      func() tako.User { return tako.User{} },
		"signedIn":  func() bool { return false },
	}
}

// renderMarkdownFunc backs {{ markdown .Body }}: sanitized (or trusted)
// HTML by construction — template.HTML is safe to return because the
// pipeline IS the guarantee.
func renderMarkdownFunc(v any) (template.HTML, error) {
	src, _ := v.(string)
	out, err := RenderMarkdown(src, siteTrustedHTML)
	if err != nil {
		return "", err
	}
	return template.HTML(out), nil
}

func requestFuncs(ctx context.Context) template.FuncMap {
	token, _ := ctx.Value(csrfTokenKey{}).(string)
	u, _ := ctx.Value(sessionUserKey{}).(*tako.User)
	return template.FuncMap{
		"csrf": func() template.HTML {
			return template.HTML(`<input type="hidden" name="_csrf" value="` + template.HTMLEscapeString(token) + `">`)
		},
		"csrfToken": func() string { return token },
		"user": func() tako.User {
			if u == nil {
				return tako.User{}
			}
			return *u
		},
		"signedIn": func() bool { return u != nil },
	}
}

// --- replacing the set at run time (FL23) -----------------------------
//
// An app that lets an editor write a template needs three things, and
// the middle one is the reason this is not a single call: a candidate
// must be RENDERABLE before it is published, not merely parseable. Go
// resolves {{ template "x" }} at execute time, so a set that lost a
// partial parses perfectly and fails on the first page that uses it.

// prepareTemplates parses a candidate set and hands it back unpublished.
// A set with any parse error is refused whole — publishing a partial set
// would install exactly the "template not found" this exists to prevent.
func prepareTemplates(fsys fs.FS) (any, error) {
	set := parseSiteTemplates(fsys)
	if set.setupErr != nil {
		return nil, set.setupErr
	}
	if len(set.parseErrs) > 0 {
		failed := make(map[string]string, len(set.parseErrs))
		for name, err := range set.parseErrs {
			failed[name] = err.Error()
		}
		return nil, &tako.TemplateErrors{Failed: failed}
	}
	return set, nil
}

// useTemplates publishes a prepared set in one atomic write.
func useTemplates(candidate any) error {
	set, ok := candidate.(*siteSet)
	if !ok || set == nil {
		return errors.New("tako: UseTemplates needs a set from PrepareTemplates on this site")
	}
	currentSet.Store(set)
	return nil
}

// renderTemplateSet and renderTemplateSetFragment render against a
// candidate WITHOUT publishing it — how an app proves a set renders
// before an editor's mistake reaches a reader.
//
// Two of them for the reason there are two of renderSite and
// renderFragment: a page renders inside the shell and a fragment renders
// itself. Handing a fragment to the page one is not a smaller check, it
// is a check that passes without executing the fragment at all — the
// layout renders, its "content" block is empty because a fragment
// defines none, and nothing reports a thing (FL27).
func renderTemplateSet(ctx context.Context, candidate any, name string, data any) ([]byte, error) {
	set, err := candidateSet(candidate, "Render")
	if err != nil {
		return nil, err
	}
	return renderIn(set, ctx, name, true, data)
}

func renderTemplateSetFragment(ctx context.Context, candidate any, name string, data any) ([]byte, error) {
	set, err := candidateSet(candidate, "Fragment")
	if err != nil {
		return nil, err
	}
	return renderIn(set, ctx, name, false, data)
}

func candidateSet(candidate any, verb string) (*siteSet, error) {
	set, ok := candidate.(*siteSet)
	if !ok || set == nil {
		return nil, errors.New("tako: " + verb + " needs a set from PrepareTemplates on this site")
	}
	return set, nil
}
