package tako

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

// Render renders the named template with data inside the site's layout.
func (s *SiteHandle) Render(ctx Ctx, template string, data any) (HTML, error) {
	panic("tako: not implemented")
}

// Fragment renders the named template with data without the layout, for
// htmx-style partial updates.
func (s *SiteHandle) Fragment(ctx Ctx, template string, data any) (HTML, error) {
	panic("tako: not implemented")
}

// Markdown renders markdown source to HTML. Output is sanitized by default
// because user-supplied markdown is an XSS vector; WithTrustedHTML on the
// site is the explicit opt-out.
func (s *SiteHandle) Markdown(ctx Ctx, src string) (HTML, error) {
	panic("tako: not implemented")
}

// HTML is the rendered result of a Page handler: markup, or a redirect
// produced by tako.Redirect.
type HTML struct {
	markup   []byte
	redirect string
}

// Redirect makes a Page handler respond with a redirect to target — a 302
// for plain requests, an HX-Redirect for htmx requests — covering the
// POST-redirect-GET pattern with one return.
func Redirect(target string) (HTML, error) {
	panic("tako: not implemented")
}
