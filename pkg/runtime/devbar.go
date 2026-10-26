package runtime

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strings"

	"github.com/gsandoval/tako/internal/routes"
)

// --- The dev sign-in bar ----------------------------------------------

// WithDevBar injects the "sign in as test user" bar into HTML documents
// — LOCAL ONLY (tako dev's promise: auth with no GCP anywhere near it).
// Cloud builds pass through untouched; the generated main stays
// identical across backends.
func WithDevBar(next http.Handler) http.Handler {
	if !isLocalBackend() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A pointer in the context for the same reason routeLabel is
		// one: the decision is made INSIDE the handler and read out
		// here, after it has returned.
		frag := &fragmentFlag{}
		r = r.WithContext(context.WithValue(r.Context(), fragmentKey{}, frag))
		bw := &barWriter{ResponseWriter: w, req: r, fragment: frag}
		next.ServeHTTP(bw, r)
		bw.flush()
	})
}

// fragmentFlag records that the response in flight is a PARTIAL — an
// htmx swap, not a document.
//
// The dev bar cannot tell from the outside: a fragment answers
// text/html like a page does. So every htmx swap under tako dev used
// to carry a copy of the bar into whatever it swapped into — in an app
// with its own fixed bottom bar, one stacking on another on every
// keystroke.
type fragmentFlag struct{ is bool }

type fragmentKey struct{}

// markFragment says this response is a partial. Called where the
// runtime already branches on exactly this — the same signal that
// decides a fragment carries no CSP.
func markFragment(r *http.Request) {
	if f, ok := r.Context().Value(fragmentKey{}).(*fragmentFlag); ok && f != nil {
		f.is = true
	}
}

type barWriter struct {
	http.ResponseWriter
	req      *http.Request
	fragment *fragmentFlag
	buf      []byte
	html     bool
	probed   bool
	status   int
}

func (b *barWriter) WriteHeader(status int) { b.status = status }

func (b *barWriter) Write(p []byte) (int, error) {
	if !b.probed {
		b.probed = true
		b.html = strings.HasPrefix(b.Header().Get("Content-Type"), "text/html")
	}
	if !b.html {
		b.commitHeader()
		return b.ResponseWriter.Write(p)
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *barWriter) commitHeader() {
	if b.status != 0 {
		b.ResponseWriter.WriteHeader(b.status)
		b.status = 0
	}
}

func (b *barWriter) flush() {
	// A fragment is not a page, so there is nothing for the bar to sit
	// at the bottom OF. Injecting into one puts a position:fixed strip
	// inside whatever the swap replaced, once per swap.
	if !b.html || (b.fragment != nil && b.fragment.is) {
		b.commitHeader()
		if len(b.buf) > 0 {
			_, _ = b.ResponseWriter.Write(b.buf)
		}
		return
	}
	// The bar REFLECTS the session instead of listing every control:
	// consistent labels, and only the actions that make sense now.
	rec := loadSession(b.req)
	who := "anonymous"
	var controls strings.Builder
	controls.WriteString(`<form method="post" action="` + routes.Session + `" style="display:inline-flex;gap:6px;margin:0">`)
	for _, u := range []string{"alice", "bob"} {
		switch {
		case rec == nil:
			fmt.Fprintf(&controls, `<button name="dev_user" value="%s">sign in as %s</button>`, u, u)
		case rec.Name != u:
			fmt.Fprintf(&controls, `<button name="dev_user" value="%s">switch to %s</button>`, u, u)
		}
	}
	controls.WriteString(`</form>`)
	if rec != nil {
		who = "signed in as " + rec.Name
		if len(declaredRoles) > 0 {
			for _, role := range declaredRoles {
				active := slices.Contains(rec.Roles, role)
				nextRoles := toggleRoleSlice(rec.Roles, role)
				controls.WriteString(`<form method="post" action="` + routes.Session + `" style="display:inline;margin:0">`)
				fmt.Fprintf(&controls, `<input type="hidden" name="dev_user" value="%s">`, html.EscapeString(rec.Name))
				controls.WriteString(`<input type="hidden" name="dev_roles_form" value="1">`)
				for _, nr := range nextRoles {
					fmt.Fprintf(&controls, `<input type="hidden" name="dev_role" value="%s">`, html.EscapeString(nr))
				}
				label := html.EscapeString(role)
				style := "opacity:0.65"
				if active {
					label = "✓ " + label
					style = "font-weight:600"
				}
				fmt.Fprintf(&controls, `<button name="dev_toggle_role" value="%s" style="%s">%s</button>`,
					html.EscapeString(role), style, label)
				controls.WriteString(`</form>`)
			}
		}
		controls.WriteString(`<form method="post" action="` + routes.SignOut + `" style="margin:0"><button>sign out</button></form>`)
	}
	// The id is a CONTRACT, not decoration: an app whose own layout puts
	// something at the bottom of the window needs a way to move or hide
	// this, and inline styles are nothing to target. Without it the only
	// handle is behaviour — "the fixed child of <body> holding a
	// dev_user control" — which is a guess at something tako never
	// promised. `#tako-dev-bar { display: none }` is now supported.
	bar := `<div id="tako-dev-bar" style="position:fixed;bottom:0;left:0;right:0;background:#1a1a1a;color:#eee;font:13px sans-serif;padding:6px 12px;display:flex;gap:12px;align-items:center;z-index:9999">` +
		`<span>tako dev · ` + who + `</span>` +
		controls.String() +
		`</div>`
	out := b.buf
	if i := strings.LastIndex(string(out), "</body>"); i >= 0 {
		out = append(append(append([]byte{}, out[:i]...), []byte(bar)...), out[i:]...)
	} else {
		out = append(out, []byte(bar)...)
	}
	b.Header().Set("Content-Length", fmt.Sprint(len(out)))
	b.commitHeader()
	_, _ = b.ResponseWriter.Write(out)
}
