package journal

// The W2 contract: server-rendered pages, browser-form binding,
// automatic CSRF, POST-redirect-GET, and pre-W4 escaping. The harness
// mirrors the generated main (runtime handlers + site templates), like
// gallery's http_test.go.

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/pkg/runtime"
)

func servePages(t *testing.T) *httptest.Server {
	t.Helper()
	if err := runtime.BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	runtime.RegisterSiteTemplates(os.DirFS("web"), runtime.SiteOptions{})
	mux := http.NewServeMux()
	mux.Handle("/api/_session", runtime.SessionHandler())
	mux.Handle("GET /{$}", runtime.PageHandler(Home, nil)) // {$}: exact root, as codegen emits
	mux.Handle("GET /p/{id}", runtime.PageHandler(Read, func(r *http.Request) (ReadReq, error) {
		return ReadReq{ID: runtime.PathString(r, "id")}, nil
	}))
	mux.Handle("GET /write", runtime.PageHandler(Write, nil))
	mux.Handle("POST /p", runtime.PageHandler(Save, func(r *http.Request) (SaveReq, error) {
		return SaveReq{
			Title: runtime.FormString(r, "title"),
			Body:  runtime.FormString(r, "body"),
		}, nil
	}, runtime.RequireAuth()))
	mux.Handle("POST /p/{id}/publish", runtime.PageHandler(Publish, func(r *http.Request) (PublishReq, error) {
		return PublishReq{ID: runtime.PathString(r, "id")}, nil
	}, runtime.RequireAuth()))
	// Harness-only: a route that ALWAYS redirects, pinning the
	// dual-mode contract (200 + HX-Redirect under htmx).
	alwaysRedirect := tako.Page("POST /redirects", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return tako.Redirect("/after")
	})
	mux.Handle("POST /redirects", runtime.PageHandler(alwaysRedirect, nil))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// client returns a cookie-jarred client that does NOT follow redirects
// (the tests judge the redirects themselves).
func client(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	c := srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	jar := srv.Client().Jar
	if jar == nil {
		var err error
		if jar, err = newJar(); err != nil {
			t.Fatal(err)
		}
	}
	c.Jar = jar
	return c
}

var tokenRe = regexp.MustCompile(`name="_csrf" value="([0-9a-f]+)"`)

// signIn mints a dev session (the dev bar's path — local backend) and
// returns the SESSION-bound CSRF token from /write.
func signIn(t *testing.T, c *http.Client, base, name string) string {
	t.Helper()
	res, err := c.PostForm(base+"/api/_session", url.Values{"dev_user": {name}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 && res.StatusCode != 200 {
		t.Fatalf("dev sign-in = %d", res.StatusCode)
	}
	return writeFlow(t, c, base)
}

// writeFlow GETs /write and returns the CSRF token the form carries.
func writeFlow(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	res, err := c.Get(base + "/write")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	m := tokenRe.FindSubmatch(body)
	if res.StatusCode != 200 || m == nil {
		t.Fatalf("/write = %d, csrf input present = %v", res.StatusCode, m != nil)
	}
	return string(m[1])
}

func TestWritePublishReadRoundTrip(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "alice")

	form := url.Values{"title": {"First post"}, "body": {"Hello, journal."}, "_csrf": {token}}
	res, err := c.PostForm(srv.URL+"/p", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode != 303 || !strings.HasPrefix(loc, "/p/") {
		t.Fatalf("save = %d → %q, want 303 → /p/<id> (POST-redirect-GET)", res.StatusCode, loc)
	}

	read, err := c.Get(srv.URL + loc)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Body.Close()
	page, _ := io.ReadAll(read.Body)
	if read.StatusCode != 200 || !strings.Contains(string(page), "First post") {
		t.Fatalf("read = %d, body misses the title", read.StatusCode)
	}
	if ct := read.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	if read.Header.Get("Content-Security-Policy-Report-Only") == "" {
		t.Fatal("rendered page misses the document security headers")
	}

	home, err := c.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer home.Body.Close()
	list, _ := io.ReadAll(home.Body)
	if !strings.Contains(string(list), loc) {
		t.Fatalf("home does not list the new post (%s)", loc)
	}
}

func TestAuthAndCSRFGates(t *testing.T) {
	srv := servePages(t)

	// Anonymous write → 401 with the exact text (WithAuth's gate).
	anon := client(t, srv)
	res, err := anon.PostForm(srv.URL+"/p", url.Values{"title": {"x"}, "body": {"y"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 401 || !strings.Contains(string(body), "sign in to continue") {
		t.Fatalf("anonymous write = %d %q, want 401", res.StatusCode, body)
	}

	// Signed in but tokenless → the CSRF 403 (session-bound token now).
	c := client(t, srv)
	signIn(t, c, srv.URL, "alice")
	res2, err := c.PostForm(srv.URL+"/p", url.Values{"title": {"x"}, "body": {"y"}})
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if res2.StatusCode != 403 || !strings.Contains(string(body2), "CSRF token missing or invalid — forms need {{ csrf }}") {
		t.Fatalf("tokenless signed-in post = %d %q", res2.StatusCode, body2)
	}
}

func TestOwnershipAndRevocation(t *testing.T) {
	srv := servePages(t)

	alice := client(t, srv)
	tokenA := signIn(t, alice, srv.URL, "alice")
	res, err := alice.PostForm(srv.URL+"/p", url.Values{"title": {"Alice's"}, "body": {"b"}, "_csrf": {tokenA}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc := res.Header.Get("Location")

	// bob may not toggle alice's post: 403 with the handler's own rule.
	bob := client(t, srv)
	tokenB := signIn(t, bob, srv.URL, "bob")
	res2, err := bob.PostForm(srv.URL+loc+"/publish", url.Values{"_csrf": {tokenB}})
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if res2.StatusCode != 403 || !strings.Contains(string(body2), "only the author can publish") {
		t.Fatalf("bob toggling alice's post = %d %q", res2.StatusCode, body2)
	}

	// Revocation is REAL: sign out (deletes the record), the old cookie dies.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/_session", nil)
	res3, err := alice.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res3.Body.Close()
	res4, err := alice.PostForm(srv.URL+"/p", url.Values{"title": {"ghost"}, "body": {"b"}, "_csrf": {tokenA}})
	if err != nil {
		t.Fatal(err)
	}
	res4.Body.Close()
	if res4.StatusCode != 401 {
		t.Fatalf("post after sign-out = %d, want 401 (session record deleted)", res4.StatusCode)
	}
}

func TestMarkdownRendersSanitized(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "alice")

	// The W6 gate's §4 judgment: an XSS payload in a post body VANISHES
	// (stripped by bluemonday) — stronger than W2's escape-to-text floor.
	body := "# Heading\n\nSome *emphasis*, a [link](https://example.com), and:\n\n" +
		"```go\nfmt.Println(1)\n```\n\n<script>alert(\"xss\")</script>\n<p onclick=\"evil()\">click</p>"
	res, err := c.PostForm(srv.URL+"/p", url.Values{"title": {"md"}, "body": {body}, "_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	read, err := c.Get(srv.URL + res.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	defer read.Body.Close()
	page, _ := io.ReadAll(read.Body)
	html := string(page)

	for _, want := range []string{
		"<h1", "<em>emphasis</em>", `rel="nofollow"`, "<pre><code", "language-go",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("markdown output missing %q", want)
		}
	}
	// (the layout's own <script src=htmx…> is fine — the ban is the
	// PAYLOAD: inline script content and handlers.)
	for _, banned := range []string{"alert(", "onclick", "&lt;script&gt;"} {
		if strings.Contains(html, banned) {
			t.Errorf("sanitizer let %q through (or fell back to escaping):\n%s", banned, html)
		}
	}
}

func TestTrustedHTMLOptOut(t *testing.T) {
	// journal never takes WithTrustedHTML; the opt-out is pinned at the
	// runtime level — raw HTML survives ONLY under the explicit posture.
	raw := `safe <script>alert(1)</script>`
	def, err := runtime.RenderMarkdown(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(def), "<script") {
		t.Fatalf("default posture kept a script tag: %s", def)
	}
	trusted, err := runtime.RenderMarkdown(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(trusted), "<script>alert(1)</script>") {
		t.Fatalf("trusted posture stripped raw HTML: %s", trusted)
	}
}

func TestUnknownPostIs404(t *testing.T) {
	srv := servePages(t)
	res, err := client(t, srv).Get(srv.URL + "/p/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("unknown post = %d, want 404", res.StatusCode)
	}
}

// newJar is a tiny cookie jar for the CSRF round-trips.
func newJar() (http.CookieJar, error) {
	return cookiejar.New(nil)
}

// --- W3: one handler, both worlds ---------------------------------------

// htmxPost posts a form with the HX-Request marker and the CSRF token
// in the header (the hx-headers path) — exactly what htmx sends.
func htmxPost(t *testing.T, c *http.Client, u string, form url.Values, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", u, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", token)
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSaveServesBothWorlds(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "alice")

	// htmx: a layoutless post-card fragment, no redirect, no doc CSP.
	res := htmxPost(t, c, srv.URL+"/p", url.Values{"title": {"Card post"}, "body": {"b"}}, token)
	defer res.Body.Close()
	frag, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(frag), "Card post") {
		t.Fatalf("htmx save = %d %q", res.StatusCode, frag)
	}
	if strings.Contains(string(frag), "<html") || strings.Contains(string(frag), "<nav") {
		t.Fatalf("fragment carries the layout:\n%s", frag)
	}
	if res.Header.Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatal("fragment carries the document CSP")
	}

	// Plain: the same handler answers 303 (pinned in the W2 test too).
	plain, err := c.PostForm(srv.URL+"/p", url.Values{"title": {"Plain post"}, "body": {"b"}, "_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	plain.Body.Close()
	if plain.StatusCode != 303 {
		t.Fatalf("plain save = %d, want 303", plain.StatusCode)
	}
}

func TestRedirectIsDualMode(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "alice")
	loc := ""
	{ // create a post to toggle
		res, err := c.PostForm(srv.URL+"/p", url.Values{"title": {"T"}, "body": {"b"}, "_csrf": {token}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		loc = res.Header.Get("Location")
	}

	// publish without HX-Request: Redirect answers 303.
	res, err := c.PostForm(srv.URL+loc+"/publish", url.Values{"_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Location") != loc {
		t.Fatalf("plain publish = %d → %q, want 303 → %s", res.StatusCode, res.Header.Get("Location"), loc)
	}

	// under htmx the toggle returns JUST the button fragment...
	frag := htmxPost(t, c, srv.URL+loc+"/publish", url.Values{}, token)
	defer frag.Body.Close()
	body, _ := io.ReadAll(frag.Body)
	if frag.StatusCode != 200 || !strings.Contains(string(body), "<button") || strings.Contains(string(body), "<html") {
		t.Fatalf("htmx toggle = %d %q", frag.StatusCode, body)
	}
	// ...and toggling twice lands back on Publish (state really flips).
	frag2 := htmxPost(t, c, srv.URL+loc+"/publish", url.Values{}, token)
	defer frag2.Body.Close()
	body2, _ := io.ReadAll(frag2.Body)
	if strings.Contains(string(body), ">Publish<") == strings.Contains(string(body2), ">Publish<") {
		t.Fatalf("toggle did not flip: %q then %q", body, body2)
	}
}

func TestHTMXRedirectHeader(t *testing.T) {
	// (harness route below has no auth requirement)
	// A handler that redirects under htmx must emit HX-Redirect + 200
	// (htmx cannot follow a 303 into a swap). read's NotFound path is
	// no use here; save without IsHTMX... so probe via a direct htmx
	// request to a route whose handler ALWAYS redirects: none exists in
	// journal, so exercise the runtime contract through save's no-JS
	// branch forced under htmx — publish on the no-JS path IS the
	// redirect; send HX-Request and the handler branches to Fragment
	// instead. The contract is therefore pinned at the runtime level:
	// craft an endpoint inline.
	srv := servePages(t)
	c := client(t, srv)
	token := writeFlow(t, c, srv.URL)
	res := htmxPost(t, c, srv.URL+"/redirects", url.Values{}, token)
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("HX-Redirect") != "/after" {
		t.Fatalf("htmx redirect = %d, HX-Redirect %q; want 200 + /after", res.StatusCode, res.Header.Get("HX-Redirect"))
	}
}

func TestFragmentEscapesLikePages(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "alice")
	res := htmxPost(t, c, srv.URL+"/p", url.Values{"title": {`<script>alert(1)</script>`}, "body": {"b"}}, token)
	defer res.Body.Close()
	frag, _ := io.ReadAll(res.Body)
	if strings.Contains(string(frag), "<script>") {
		t.Fatal("fragment rendered a script tag raw")
	}
}
