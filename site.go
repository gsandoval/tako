package tako

import (
	"errors"

	"github.com/gsandoval/tako/internal/hooks"
)

// Site declares the app's web-serving story rooted at dir: static assets,
// templates (in dir/templates), frontend builds (WithBuild, WithDevProxy),
// caching and security headers, domains, and redirects. v0 embeds the site
// in the app service's binary; WithCDN upgrades serving when scale
// justifies it.
func Site(dir string, opts ...Option) *SiteHandle {
	return &SiteHandle{dir: dir, opts: opts}
}

// SiteHandle is the app's declared site.
type SiteHandle struct {
	dir  string
	opts []Option
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
	return HTML{}, errors.New("tako: Fragment arrives with W3 (fragments + htmx) — Render serves whole pages until then")
}

// Markdown renders markdown source to HTML. Output is sanitized by default
// because user-supplied markdown is an XSS vector; WithTrustedHTML on the
// site is the explicit opt-out.
func (s *SiteHandle) Markdown(ctx Ctx, src string) (HTML, error) {
	return HTML{}, errors.New("tako: Markdown arrives with W4 — render bodies escaped until then")
}

// HTML is the rendered result of a Page handler: markup, or a redirect
// produced by tako.Redirect.
type HTML struct {
	markup   []byte
	redirect string
}

// Redirect makes a Page handler respond with a redirect to target — a
// 303 See Other for plain requests (the POST-redirect-GET status), an
// HX-Redirect for htmx requests (W3) — covering the pattern with one
// return.
func Redirect(target string) (HTML, error) {
	return HTML{redirect: target}, nil
}
