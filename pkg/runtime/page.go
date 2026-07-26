package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
)

// csrfTokenKey carries the request's CSRF token to the template funcs.
type csrfTokenKey struct{}

// csrfCookie is the double-submit cookie; when auth lands (W5) the
// token binds to the session instead.
const csrfCookie = "__tako_csrf"

// csrfRejection is the exact 403 text — product UX, pinned by tests.
const csrfRejection = "CSRF token missing or invalid — forms need {{ csrf }}"

// siteTemplates is the parsed template set: one pre-parsed clone per
// page template, each sharing layout.html. Parsed once per process
// (dev restarts per cycle; embedded content is immutable per binary).
var siteTemplates map[string]*template.Template

// RegisterSiteTemplates parses <site>/templates for site.Render:
// layout.html is the shell, every other *.html fills its "content"
// block. Generated mains call this with the embedded site FS. Parse
// errors are recorded per template and surface at render time with the
// template named — the dev loop iterates on exactly that text.
func RegisterSiteTemplates(fsys fs.FS) {
	hooks.RenderSite = renderSite
	hooks.RenderFragment = renderFragment
	siteTemplates = map[string]*template.Template{}
	siteParseErrors = map[string]error{}

	entries, err := fs.ReadDir(fsys, "templates")
	if err != nil {
		siteSetupError = errors.New("the site has no templates/ directory — site.Render needs web/templates/layout.html plus one file per page")
		return
	}
	layout, layoutErr := template.New("layout.html").Funcs(siteFuncs()).ParseFS(fsys, "templates/layout.html")
	if layoutErr != nil {
		siteSetupError = fmt.Errorf("site templates: %v (layout.html is the shell every page renders inside)", layoutErr)
		return
	}
	siteSetupError = nil

	// Two tiers: PAGES define the layout's "content" block (each gets
	// its own clone — their block names would collide in one set);
	// PARTIALS don't, and parse into the shared base so any page or
	// fragment can {{ template }} them (post cards, toggles).
	var pages, partials []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == "layout.html" || !strings.HasSuffix(name, ".html") {
			continue
		}
		raw, err := fs.ReadFile(fsys, "templates/"+name)
		if err != nil {
			siteParseErrors[name] = err
			continue
		}
		if definesContent(string(raw)) {
			pages = append(pages, name)
		} else {
			partials = append(partials, name)
		}
	}
	for _, name := range partials {
		if _, err := layout.ParseFS(fsys, "templates/"+name); err != nil {
			siteParseErrors[name] = err
		}
	}
	for _, name := range partials {
		if siteParseErrors[name] == nil {
			siteTemplates[name] = layout // fragments render from the shared base
		}
	}
	for _, name := range pages {
		t, err := layout.Clone()
		if err == nil {
			_, err = t.ParseFS(fsys, "templates/"+name)
		}
		if err != nil {
			siteParseErrors[name] = err
			continue
		}
		siteTemplates[name] = t
	}
}

var definesContentRe = regexp.MustCompile(`\{\{-?\s*(define|block)\s+"content"`)

func definesContent(src string) bool { return definesContentRe.MatchString(src) }

var (
	siteParseErrors = map[string]error{}
	siteSetupError  error
)

func renderSite(ctx context.Context, name string, data any) ([]byte, error) {
	return renderNamed(ctx, name, "layout.html", data)
}

// renderFragment executes the named template itself — no layout, the
// htmx partial path. Same template set, same funcs, same escaping.
func renderFragment(ctx context.Context, name string, data any) ([]byte, error) {
	return renderNamed(ctx, name, name, data)
}

func renderNamed(ctx context.Context, name, root string, data any) ([]byte, error) {
	if siteSetupError != nil {
		return nil, siteSetupError
	}
	if err, ok := siteParseErrors[name]; ok {
		return nil, fmt.Errorf("template %s: %v", name, err)
	}
	t, ok := siteTemplates[name]
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
		"markdown": func(any) (template.HTML, error) {
			return "", errors.New("{{ markdown }} arrives with W4 — render bodies escaped until then")
		},
	}
}

func requestFuncs(ctx context.Context) template.FuncMap {
	token, _ := ctx.Value(csrfTokenKey{}).(string)
	return template.FuncMap{
		"csrf": func() template.HTML {
			return template.HTML(`<input type="hidden" name="_csrf" value="` + template.HTMLEscapeString(token) + `">`)
		},
		"csrfToken": func() string { return token },
	}
}

// PageHandler wraps a Page endpoint: bind the request (path/query/form
// tags), enforce CSRF on unsafe methods, invoke, and render — markup as
// a document (same header policy as the static site), tako.Redirect as
// 303 See Other (the POST-redirect-GET status). Errors render plain
// (styled error pages are a later slice): explicit statuses as-is,
// everything else an opaque 500 with the request id logged.
func PageHandler[Req any](e tako.Endpoint[Req, tako.HTML], decode func(*http.Request) (Req, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := newRequestID()
		token, minted := csrfEnsure(w, r)
		if unsafeMethod(r.Method) {
			if minted || !csrfValid(r, token) {
				http.Error(w, csrfRejection, http.StatusForbidden)
				return
			}
		}
		rctx := context.WithValue(r.Context(), csrfTokenKey{}, token)
		// IsHTMX is the HX-Request header, nothing else (the F10 fence:
		// a request-shape probe — the framework never switches modes on
		// it by itself; handlers do).
		ctx := &requestCtx{Context: rctx, log: slog.Default().With("req", reqID),
			isHTMX: r.Header.Get("HX-Request") == "true"}

		var req Req
		if decode != nil {
			var err error
			if req, err = decode(r); err != nil {
				pageError(w, ctx, reqID, err)
				return
			}
		}
		res, err := e.Call(ctx, req)
		if err != nil {
			pageError(w, ctx, reqID, err)
			return
		}
		markup, redirect, fragment := hooks.HTMLParts(res)
		if redirect != "" {
			// Dual-mode: htmx cannot follow a 303 into a swap, so it
			// gets 200 + HX-Redirect (full-page client navigation);
			// plain requests keep the POST-redirect-GET 303.
			if ctx.isHTMX {
				w.Header().Set("HX-Redirect", redirect)
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Redirect(w, r, redirect, http.StatusSeeOther)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		if !fragment {
			// Documents carry the CSP; fragments are partials injected
			// into a page that already has one.
			h.Set("Content-Security-Policy-Report-Only", "default-src 'self'")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		}
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

func csrfValid(r *http.Request, token string) bool {
	sent := r.Header.Get("X-CSRF-Token")
	if sent == "" {
		sent = r.PostFormValue("_csrf")
	}
	return sent != "" && subtle.ConstantTimeCompare([]byte(sent), []byte(token)) == 1
}

// pageError renders errors as plain text pages: explicit statuses
// (NotFound, decode 400s) as-is; everything else an opaque 500 — the
// real error is logged with the request id, never sent to the client.
func pageError(w http.ResponseWriter, ctx tako.Ctx, reqID string, err error) {
	if he, ok := err.(interface{ HTTPStatus() int }); ok {
		http.Error(w, err.Error(), he.HTTPStatus())
		return
	}
	ctx.Log().Error("page error", "err", err)
	http.Error(w, "internal error (req "+reqID+")", http.StatusInternalServerError)
}
