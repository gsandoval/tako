package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
)

// csrfTokenKey carries the request's CSRF token to the template funcs.
type csrfTokenKey struct{}

// sessionUserKey carries the signed-in user to the template funcs.
type sessionUserKey struct{}

// HandlerOption tunes a generated handler wrapper.
type HandlerOption func(*handlerConf)

type handlerConf struct {
	requireAuth bool
	// policy is the NAME of the route's declared tako.Policy. The check
	// itself is not here: Endpoint.Call enforces it, so one rule runs at
	// one place whether the caller is a browser or another service. What
	// the mount adds is the name, which is what a status page needs to
	// tell this refusal from a stale CSRF token's.
	policy string
	// maxAge is Route.MaxAge in seconds: how long a shared cache may
	// keep this page. Zero is no-cache, which is every page that does
	// not ask otherwise.
	maxAge int
}

// RequireAuth enforces Route.SignIn: no valid session → 401 before
// the handler runs; past it, ctx.User() is guaranteed.
func RequireAuth() HandlerOption {
	return func(c *handlerConf) { c.requireAuth = true }
}

// RequirePolicy names the route's declared tako.Policy (Route.Allow) so
// a refusal from it can be told apart from every other 403 — it arrives
// at the status page as Status.Policy.
//
// It carries the name and not the handle deliberately: the policy is
// evaluated by Endpoint.Call, which is the ONE enforcement point for
// browsers, in-process callers and cross-service calls alike. A second
// check here would run an app's rule twice per request, and a rule that
// reads the entity store is not free.
func RequirePolicy(name string) HandlerOption {
	return func(c *handlerConf) { c.policy = name }
}

// CacheFor sets Route.MaxAge on a generated page handler: the response
// may be kept by shared caches for that many seconds. Without it every
// rendered page answers no-cache, which makes a read-heavy site
// revalidate every visitor's request against the app and its store.
//
// The analyzer refuses it on a SignIn route (tako/cache-signin), so a
// public cache can never be handed an authenticated page.
func CacheFor(seconds int) HandlerOption {
	return func(c *handlerConf) { c.maxAge = seconds }
}

// csrfCookie is the double-submit cookie; when auth lands (W5) the
// token binds to the session instead.
const csrfCookie = "__tako_csrf"

// csrfRejection is the exact 403 text — product UX, pinned by tests.
const csrfRejection = "CSRF token missing or invalid — forms need {{ csrf }}"

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

// PageHandler wraps a Page endpoint: bind the request (path/query/form
// tags), enforce CSRF on unsafe methods, invoke, and render — markup as
// a document (same header policy as the static site), tako.Redirect as
// 303 See Other (the POST-redirect-GET status). Errors render plain
// (styled error pages are a later slice): explicit statuses as-is,
// everything else an opaque 500 with the request id logged.
func PageHandler[Req any](e tako.Endpoint[Req, tako.HTML], decode func(*http.Request) (Req, error), opts ...HandlerOption) http.Handler {
	var conf handlerConf
	for _, o := range opts {
		o(&conf)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := incomingRequestID(r)
		// The session (when present) carries the user AND the CSRF
		// truth: a stolen anonymous token cannot ride a signed-in
		// session (W5). Anonymous browsers keep the double-submit
		// cookie from W2.
		session := loadSession(r)
		if conf.requireAuth && session == nil {
			// A PAGE's 401 is read by a person, so it renders like every
			// other answer tako gives (FL11). The API's stays JSON —
			// http.go writes that one, and a client parses it.
			writeStatus(w, r, tako.Status{Code: http.StatusUnauthorized, RequestID: reqID, Message: authRequired})
			return
		}
		token := ""
		if session != nil {
			token = session.CSRF
			if unsafeMethod(r.Method) && !csrfValidAgainst(r, token) {
				writeStatus(w, r, tako.Status{Code: http.StatusForbidden, RequestID: reqID, Message: csrfRejection})
				return
			}
		} else if conf.maxAge > 0 && !unsafeMethod(r.Method) {
			// A page declared cacheable is a page whose response is the
			// same for everybody, so it must carry no per-visitor state
			// — and a CSRF token is exactly that. Minting one here would
			// bake one visitor's token into a page a shared cache hands
			// to the next.
			//
			// It also makes the header REACHABLE: Cloud Run's frontend
			// downgrades public to private whenever a response carries a
			// Set-Cookie, so a cacheable page that minted a cookie was
			// never publicly cached on the visit that matters. Measured
			// live before this existed:
			//
			//	cache-control: private, max-age=300   (first visit, Set-Cookie)
			//	cache-control: public, max-age=300    (cookie already held)
			//
			// The trade is that {{ csrf }} renders empty on such a page,
			// so a cacheable page cannot carry a form. That is the same
			// bargain as tako/cache-signin, and the same reason.
		} else {
			var minted bool
			token, minted = csrfEnsure(w, r)
			if unsafeMethod(r.Method) {
				if minted || !csrfValidAgainst(r, token) {
					writeStatus(w, r, tako.Status{Code: http.StatusForbidden, RequestID: reqID, Message: csrfRejection})
					return
				}
			}
		}
		// Cross-service context (W9): session + trace ids ride as
		// values so outgoing Calls propagate them; a propagated
		// deadline bounds this handler too.
		base, cancelDeadline := callContext(r, session, reqID)
		defer cancelDeadline()
		rctx := context.WithValue(base, csrfTokenKey{}, token)
		// IsHTMX is the HX-Request header, nothing else (the F10 fence:
		// a request-shape probe — the framework never switches modes on
		// it by itself; handlers do).
		ctx := &requestCtx{Context: rctx, log: requestLogger(r, reqID),
			client: clientOf(r),
			isHTMX: r.Header.Get("HX-Request") == "true"}
		if session != nil {
			u := session.user()
			ctx.user = &u
			rctx = context.WithValue(rctx, sessionUserKey{}, ctx.user)
			ctx.Context = rctx
		}

		var req Req
		if decode != nil {
			var err error
			if req, err = decode(r); err != nil {
				pageError(w, r, ctx, reqID, err, conf.policy)
				return
			}
		}
		res, err := e.Call(ctx, req)
		if err != nil {
			pageError(w, r, ctx, reqID, err, conf.policy)
			return
		}
		markup, redirect, status, fragment := hooks.HTMLParts(res)
		if redirect != "" {
			// Dual-mode: htmx cannot follow a 303 into a swap, so it
			// gets 200 + HX-Redirect (full-page client navigation);
			// plain requests keep the POST-redirect-GET 303.
			//
			// The htmx arm is deliberately identical for a PERMANENT
			// redirect. Permanence is a statement to crawlers and
			// caches about a URL; a client-side navigation is neither,
			// and a 301 htmx cannot follow would be a regression
			// dressed as correctness.
			if ctx.isHTMX {
				w.Header().Set("HX-Redirect", redirect)
				w.WriteHeader(http.StatusOK)
				return
			}
			if status == 0 {
				status = http.StatusSeeOther
			}
			http.Redirect(w, r, redirect, status)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		// A page that declared no MaxAge answers no-cache, which is
		// what every page did before Route.MaxAge existed.
		h.Set("Cache-Control", cacheControl(conf.maxAge))
		if fragment {
			// The same signal, told to the one other thing that needs
			// it: the dev bar belongs on documents, not on partials.
			markFragment(r)
		}
		securityHeaders(h, fragment)
		_, _ = w.Write(markup)
	})
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// csrfEnsure returns the browser's token, minting (and setting) one
// when absent. minted=true on a first contact — an unsafe request
// cannot possibly carry a matching token then.
func csrfEnsure(w http.ResponseWriter, r *http.Request) (string, bool) {
	if c, err := r.Cookie(csrfCookie); err == nil && c.Value != "" {
		return c.Value, false
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	token := hex.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return token, true
}

func csrfValidAgainst(r *http.Request, token string) bool {
	sent := r.Header.Get("X-CSRF-Token")
	if sent == "" {
		sent = r.PostFormValue("_csrf")
	}
	return csrfMatches(sent, token)
}

// pageError renders errors as pages: explicit statuses (NotFound, decode
// 400s) as-is; everything else an opaque 500 — the real error is logged
// with the request id, never sent to the client.
//
// EVERY status goes through the app's status page now, not just 404
// (FL11). The old rule — render for a 404, plain text for anything
// else, because a 500 means the process is already broken and running
// the template engine is the worst moment to depend on it — was right
// about the risk and wrong about the remedy: writeStatus catches a
// panicking status page and falls through to the same plain text. The
// downside is bounded, and the upside is that a 403 and a 500 stop
// being the two places a site drops its own skin.
func pageError(w http.ResponseWriter, r *http.Request, ctx tako.Ctx, reqID string, err error, policy string) {
	if he, ok := err.(interface{ HTTPStatus() int }); ok {
		// The policy name rides along only on a policy's refusal. A
		// stale CSRF token is also a 403 on the same route, and a status
		// page that could not tell them apart would send a signed-in
		// editor to ask for access they already have.
		named := ""
		if tako.IsDenied(err) {
			named = policy
		}
		writeStatus(w, r, tako.Status{Code: he.HTTPStatus(), RequestID: reqID, Message: err.Error(), Policy: named})
		return
	}
	ctx.Log().Error("page error", "err", err)
	writeStatus(w, r, tako.Status{Code: http.StatusInternalServerError, RequestID: reqID})
}

// (The 404 writers that used to live here are one writer now, in
// status.go — every response tako produces for itself goes through
// writeStatus, so its headers and its fallback are decided once.)

// cacheControl renders the page's caching posture. "public" is the
// point of the setting — a private max-age would only help the one
// browser that already has the page.
func cacheControl(maxAge int) string {
	if maxAge <= 0 {
		return "no-cache"
	}
	return fmt.Sprintf("public, max-age=%d", maxAge)
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

// renderTemplateSet renders against a candidate WITHOUT publishing it —
// how an app proves a set renders before an editor's mistake reaches a
// reader.
func renderTemplateSet(ctx context.Context, candidate any, name string, data any) ([]byte, error) {
	set, ok := candidate.(*siteSet)
	if !ok || set == nil {
		return nil, errors.New("tako: Render needs a set from PrepareTemplates on this site")
	}
	return renderIn(set, ctx, name, true, data)
}
