package tako

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/gsandoval/tako/internal/hooks"
)

// Site declares the app's web-serving story rooted at dir: static assets,
// templates (in dir/templates), frontend builds (SiteConfig.Build, DevProxy),
// caching and security headers, and redirects. The site is embedded in the
// app service's binary — one service, one URL — so a site's static assets
// and its dynamic handlers always answer on the same origin. ServiceConfig.Domain puts
// that origin on a custom domain.
func Site(dir string, cfg ...SiteConfig) *SiteHandle {
	return &SiteHandle{dir: dir, cfg: cfg}
}

// SiteHandle is the app's declared site.
type SiteHandle struct {
	dir string
	cfg []SiteConfig
}

// Render renders the named template with data inside the site's layout
// (web/templates/layout.html; the page fills its "content" block).
func (s *SiteHandle) Render(ctx Ctx, template string, data any) (HTML, error) {
	if hooks.RenderSite == nil {
		return HTML{}, errors.New("tako: site templates are not wired — Render works in generated services (tako dev / deploy), not bare tests")
	}
	markup, err := hooks.RenderSite(ctx, template, data)
	if err != nil {
		return HTML{}, err
	}
	return HTML{markup: markup}, nil
}

// Fragment renders the named template with data without the layout, for
// htmx-style partial updates.
func (s *SiteHandle) Fragment(ctx Ctx, template string, data any) (HTML, error) {
	if hooks.RenderFragment == nil {
		return HTML{}, errors.New("tako: site templates are not wired — Fragment works in generated services (tako dev / deploy), not bare tests")
	}
	markup, err := hooks.RenderFragment(ctx, template, data)
	if err != nil {
		return HTML{}, err
	}
	return HTML{markup: markup, fragment: true}, nil
}

// Markdown renders markdown source to HTML. Output is sanitized by default
// because user-supplied markdown is an XSS vector; SiteConfig.TrustedHTML on the
// site is the explicit opt-out. The result serves as a fragment (htmx
// preview endpoints render it directly).
func (s *SiteHandle) Markdown(ctx Ctx, src string) (HTML, error) {
	if hooks.RenderMarkdown == nil {
		return HTML{}, errors.New("tako: site templates are not wired — Markdown works in generated services (tako dev / deploy), not bare tests")
	}
	markup, err := hooks.RenderMarkdown(src)
	if err != nil {
		return HTML{}, err
	}
	return HTML{markup: markup, fragment: true}, nil
}

// HTML is the rendered result of a Page handler: markup (a document, or
// a layoutless fragment), or a redirect produced by tako.Redirect or
// tako.RedirectPermanent.
type HTML struct {
	markup   []byte
	redirect string
	// status is the redirect status for plain requests. Zero means
	// 303 See Other, so Redirect needs no change and a handler that
	// returns a bare HTML{} is not accidentally a redirect.
	status   int
	fragment bool
}

// Redirect makes a Page handler respond with a redirect to target — a
// 303 See Other for plain requests (the POST-redirect-GET status), an
// HX-Redirect for htmx requests (W3) — covering the pattern with one
// return.
func Redirect(target string) (HTML, error) {
	return HTML{redirect: target}, nil
}

// RedirectPermanent is 301 Moved Permanently: this URL is not coming
// back, and a crawler should update its index.
//
//	return tako.RedirectPermanent("/p/" + moved.To)
//
// The difference from Redirect is not cosmetic and not for browsers —
// both follow either one. It is for crawlers and caches, which treat 301
// as a fact about the URL and 303 as a fact about one response. Use it
// when a resource MOVED; use Redirect for the POST-redirect-GET after a
// write, which is about this request and not about the URL.
//
// SiteConfig.Redirects covers the same ground for routes known at
// analysis time. This is for the ones that are not: a slug the user
// renamed is a redirect whose target lives in the entity store and can
// only be decided per request.
//
// htmx is unchanged — HX-Redirect and 200, exactly as Redirect —
// because permanence means nothing to a client that is not a crawler,
// and a client-side navigation is not a cacheable response about a URL.
func RedirectPermanent(target string) (HTML, error) {
	return HTML{redirect: target, status: 301}, nil
}

// PrepareTemplates parses fsys as a complete candidate template set and
// returns it WITHOUT installing it. The set is the whole of
// <site>/templates: a candidate is a set, not a file, because templates
// reference each other.
//
// A candidate with any parse error is refused whole, and the error is a
// *TemplateErrors naming every template that failed — an editor gets all
// their mistakes at once rather than one per save.
//
// Parsing clean is NOT proof the set renders: Go resolves
// {{ template "card.html" }} at execute time, so a set that lost a
// partial parses perfectly and fails on the first page that uses it.
// That is what TemplateSet.Render is for — render every surface you
// care about against the candidate, and call UseTemplates only if they
// all come back.
func (s *SiteHandle) PrepareTemplates(ctx Ctx, fsys fs.FS) (*TemplateSet, error) {
	if hooks.PrepareTemplates == nil {
		return nil, errors.New("tako: site templates are not wired — PrepareTemplates works in generated services (tako dev / deploy), not bare tests")
	}
	set, err := hooks.PrepareTemplates(fsys)
	if err != nil {
		return nil, err
	}
	return &TemplateSet{impl: set}, nil
}

// TemplateSet is a parsed candidate set, not yet serving. Render it to
// find out whether it works; UseTemplates publishes it.
type TemplateSet struct {
	impl any
}

// Render renders the named template inside the candidate's layout,
// exactly as SiteHandle.Render would — against this set instead of the
// one currently serving, and without publishing anything.
func (set *TemplateSet) Render(ctx Ctx, template string, data any) (HTML, error) {
	if hooks.RenderTemplateSet == nil {
		return HTML{}, errors.New("tako: site templates are not wired — Render works in generated services (tako dev / deploy), not bare tests")
	}
	if set == nil {
		return HTML{}, errors.New("tako: this template set was never prepared")
	}
	markup, err := hooks.RenderTemplateSet(ctx, set.impl, template, data)
	if err != nil {
		return HTML{}, err
	}
	return HTML{markup: markup}, nil
}

// UseTemplates publishes a prepared set: every render after it uses the
// new templates, and every render already under way finishes against the
// set it started with.
//
// The swap is per PROCESS. A deployed service that scales to more than
// one instance publishes into the instance that served this request, and
// the others keep serving what they booted with until they restart —
// which is why an app that replaces templates should load its stored set
// at boot, so a restart converges rather than reverting.
func (s *SiteHandle) UseTemplates(ctx Ctx, set *TemplateSet) error {
	if hooks.UseTemplates == nil {
		return errors.New("tako: site templates are not wired — UseTemplates works in generated services (tako dev / deploy), not bare tests")
	}
	if set == nil {
		return errors.New("tako: UseTemplates needs a set from PrepareTemplates")
	}
	return hooks.UseTemplates(set.impl)
}

// TemplateErrors reports which templates in a refused candidate set
// failed to parse, and why. Keyed by path relative to templates/ — the
// name the editor typed, and the name Render takes.
//
// The values are strings rather than errors because their destination is
// a person: an admin renders them back to whoever made the mistake, and
// often serializes them on the way.
type TemplateErrors struct {
	Failed map[string]string
}

func (e *TemplateErrors) Error() string {
	names := make([]string, 0, len(e.Failed))
	for name := range e.Failed {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 1 {
		return fmt.Sprintf("template %s: %s", names[0], e.Failed[names[0]])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d templates did not parse, and none of the set was published:", len(names))
	for _, name := range names {
		fmt.Fprintf(&b, "\n  %s: %s", name, e.Failed[name])
	}
	return b.String()
}
