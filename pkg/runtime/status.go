package runtime

// The one place tako answers for itself.
//
// A response tako produces rather than a handler — a URL matching no
// route, an expired CSRF token, a method the site does not serve, a
// handler's error — used to be written in four different places, with
// three different header sets and two different fallbacks. Which one a
// visitor got depended on which code path noticed first:
//
//	writeNotFoundPage   CSP + Referrer-Policy + nosniff, template render
//	writeStaticNotFound nosniff only, static file
//	http.NotFound       nothing, "404 page not found"
//	http.Error          nothing, the message as text/plain
//
// The security headers differing by whether a declared 404 resolved as
// a template or as a static file is not a thing anyone decided. FL11
// makes it one writer: headers decided once, the app's own page when
// it declared one, the same plain-text floor when it did not.

import (
	"context"
	"fmt"
	"net/http"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
)

// statusPage is the app's declared status page, installed by the
// generated main. Package state for the same reason the site renderers
// are: every path that answers for tako has to reach it, and they are
// reached from different packages at different moments.
var statusPage tako.StatusHandle

// RegisterStatusPage installs the app's tako.StatusPage. Generated
// mains call it; there is exactly one per service.
func RegisterStatusPage(h tako.StatusHandle) { statusPage = h }

// fallbackNotFound is the site's declared 404 template name, when the
// app named one instead of declaring a status page. It stays because
// SiteConfig.NotFound is the easy spelling and nothing about it
// changed — it is now the shorthand, and the status page is the seam.
var fallbackNotFound string

// statusText is the plain-text floor, and the reason a status page can
// never make things worse.
func statusText(s tako.Status) string {
	if s.Message != "" {
		return s.Message
	}
	if s.Code >= 500 && s.RequestID != "" {
		return fmt.Sprintf("internal error (req %s)", s.RequestID)
	}
	return http.StatusText(s.Code)
}

// writeStatus answers a request tako is handling itself.
//
// It tries the app's status page, then the site's declared 404 (for a
// 404 only — that is what SiteConfig.NotFound means), then plain text.
// Every failure falls through: a status page that panics, errors or
// renders nothing costs the visitor nothing they had not already lost.
// This is the generalisation of FL2's rule — a 404 never becomes a
// 500 — to every status tako answers.
func writeStatus(w http.ResponseWriter, r *http.Request, s tako.Status) {
	if s.Path == "" && r != nil {
		s.Path = r.URL.Path
	}
	if s.Code >= 500 {
		// A 5xx's detail is logged against the request id. Nothing the
		// visitor sees may carry it, so it cannot be carried here.
		s.Message = ""
	}
	if markup, ok := renderStatus(r, s); ok {
		writeStatusMarkup(w, s.Code, markup)
		return
	}
	if s.Code == http.StatusNotFound && fallbackNotFound != "" {
		if markup, err := renderSite(requestContext(r), fallbackNotFound, nil); err == nil && len(markup) > 0 {
			writeStatusMarkup(w, s.Code, markup)
			return
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(s.Code)
	fmt.Fprintln(w, statusText(s))
}

// renderStatus calls the declared page, and survives it. A status page
// runs at the worst moments — the 500 path exists because something in
// this process is already broken — so a panic in it is caught here
// rather than taking the response with it.
func renderStatus(r *http.Request, s tako.Status) (markup []byte, ok bool) {
	if !statusPage.Declared() {
		return nil, false
	}
	defer func() {
		if p := recover(); p != nil {
			markup, ok = nil, false
		}
	}()
	ctx := statusCtx(r)
	html, err := statusPage.Render(ctx, s)
	if err != nil {
		ctx.Log().Error("status page failed", "err", err, "code", s.Code)
		return nil, false
	}
	// The SDK's HTML keeps its parts unexported; the runtime reads them
	// through the same hook the page handler uses.
	body, _, _, _ := hooks.HTMLParts(html)
	if len(body) == 0 {
		return nil, false
	}
	return body, true
}

// writeStatusMarkup is the header set every tako-rendered page carries.
// One list, one place — which is the cleanup: these used to differ by
// which function happened to answer.
func writeStatusMarkup(w http.ResponseWriter, code int, markup []byte) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy-Report-Only", "default-src 'self'")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.WriteHeader(code)
	_, _ = w.Write(markup)
}

// statusCtx builds the Ctx a status page runs with: the same request
// logger and session a page handler would have had, so a status page
// can greet a signed-in visitor by name and its log lines correlate
// with the request that produced them.
//
// It is built here rather than threaded from the call sites because
// the call sites do not agree on what they have — the site handler
// holds only a request, and a 404 from a static miss never built a Ctx
// at all.
func statusCtx(r *http.Request) tako.Ctx {
	if r == nil {
		return NewCtx(context.Background())
	}
	reqID := incomingRequestID(r)
	session := loadSession(r)
	ctx := &requestCtx{
		Context: r.Context(),
		log:     requestLogger(r, reqID),
		isHTMX:  r.Header.Get("HX-Request") == "true",
	}
	if session != nil {
		ctx.user = &tako.User{ID: session.UID, Email: session.Email, Name: session.Name}
	}
	return ctx
}

// requestContext is the plain context behind a request, or a
// background one — the site's 404 path can be reached without one.
func requestContext(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return r.Context()
}
