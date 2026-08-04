package tako

import (
	"errors"

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
