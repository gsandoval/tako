package journal

// The W2 contract: server-rendered pages, browser-form binding,
// automatic CSRF, POST-redirect-GET, and pre-W4 escaping. The harness
// mirrors the generated main (runtime handlers + site templates), like
// gallery's http_test.go.

import (
	"errors"
	"fmt"
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
	// Mirrors what codegen emits from the app's tako.Site declaration.
	// The 404 page has to be here for the same reason it is there: both
	// halves of the feature — a handler's tako.NotFound() and the site
	// handler's static miss — read it from the wired options.
	siteOpts := runtime.SiteOptions{
		Redirects: map[string]string{"/about-us": "/about"},
	}
	runtime.RegisterSiteTemplates(os.DirFS("web"), siteOpts)
	// The declared status page, exactly as codegen registers it. journal
	// answers every status through it since FL11 — SiteConfig.NotFound
	// is the other spelling and this app no longer declares it, so
	// without this line every 404 here falls back to plain text.
	runtime.RegisterStatusPage(Status)
	// Two muxes, exactly as codegen emits them: tako's reserved
	// endpoints answer before the app's router is consulted. On one
	// shared mux the session mount and Perma's wildcard below cannot
	// both be registered — Go refuses to order them and panics — which
	// is the whole of FL5.
	takoMux := http.NewServeMux()
	takoMux.Handle("/_tako/api/session", runtime.SessionHandler())
	takoMux.Handle("GET /_tako/signin", runtime.SignInPage("google"))
	takoMux.Handle("POST /_tako/api/session/signout", runtime.SignOutHandler())
	mux := http.NewServeMux()
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
	mux.Handle("POST /p/{id}/rename", runtime.PageHandler(Rename, func(r *http.Request) (RenameReq, error) {
		return RenameReq{ID: runtime.PathString(r, "id"), Slug: runtime.FormString(r, "slug")}, nil
	}, runtime.RequireAuth()))
	mux.Handle("POST /p/unpublish", runtime.PageHandler(Unpublish, func(r *http.Request) (UnpublishReq, error) {
		// FormStrings, not FormString: the field repeats, and this is
		// the line codegen emits for a []string binding.
		return UnpublishReq{Selected: runtime.FormStrings(r, "selected")}, nil
	}, runtime.RequireAuth()))
	// Harness-only: a route that ALWAYS redirects, pinning the
	// dual-mode contract (200 + HX-Redirect under htmx).
	alwaysRedirect := tako.Page("POST /redirects", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return tako.Redirect("/after")
	})
	mux.Handle("POST /redirects", runtime.PageHandler(alwaysRedirect, nil))
	// The same, permanently. A 301 and a 303 differ only in the status,
	// which is exactly why nothing would notice if the wiring dropped it.
	alwaysMoved := tako.Page("POST /moved", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return tako.RedirectPermanent("/elsewhere")
	})
	mux.Handle("POST /moved", runtime.PageHandler(alwaysMoved, nil))
	// Harness-only: a route that always fails, so the 500 path can be
	// held to plain text.
	alwaysBroken := tako.Page("GET /boom", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return tako.HTML{}, errors.New("something came apart")
	})
	mux.Handle("GET /boom", runtime.PageHandler(alwaysBroken, nil))
	// The site takes everything the declared routes did not, exactly as
	// the generated main mounts it — which is what makes a mistyped URL
	// a SITE 404 rather than the mux's bare one.
	mux.Handle("GET /{year}/{slug}", runtime.PageHandler(Perma, func(r *http.Request) (PermaReq, error) {
		return PermaReq{Year: runtime.PathString(r, "year"), Slug: runtime.PathString(r, "slug")}, nil
	}))
	mux.Handle("/", runtime.SiteHandler(os.DirFS("web"), siteOpts))
	srv := httptest.NewServer(runtime.Reserved(takoMux, mux))
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
	res, err := c.PostForm(base+"/_tako/api/session", url.Values{"dev_user": {name}})
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

	// Anonymous write → 401 with the exact text (Route{SignIn: true}'s gate).
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
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/_tako/api/session", nil)
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

func TestSignInPageDevFlow(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	res, err := c.Get(srv.URL + "/_tako/signin?next=/write")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	page, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(page), `name="dev_user" value="alice"`) {
		t.Fatalf("dev sign-in page = %d %q", res.StatusCode, page)
	}
	// The dev form lands on next after minting the session.
	res2, err := c.PostForm(srv.URL+"/_tako/api/session", url.Values{"dev_user": {"alice"}, "next": {"/write"}})
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != 303 || res2.Header.Get("Location") != "/write" {
		t.Fatalf("dev sign-in = %d → %q, want 303 → /write", res2.StatusCode, res2.Header.Get("Location"))
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

// A renamed post keeps answering at its old slug — with a 301, so a
// crawler updates its index and a shared link survives.
//
// This is the case SiteConfig.Redirects cannot cover: that map is read
// at analysis time, and no analysis knows a slug someone will type next
// week. The target here comes out of the entity store, per request.
func TestRenamedPostRedirectsPermanentlyFromItsOldSlug(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "ada")

	res0, err := c.PostForm(srv.URL+"/p", url.Values{
		"title": {"Moving day"}, "body": {"body"}, "_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	res0.Body.Close()
	id := strings.TrimPrefix(res0.Header.Get("Location"), "/p/")

	ren, err := c.PostForm(srv.URL+"/p/"+id+"/rename", url.Values{
		"slug": {"moving-day"}, "_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	ren.Body.Close()
	if ren.StatusCode != http.StatusSeeOther {
		t.Fatalf("rename = %d, want 303 — the write itself is a POST-redirect-GET", ren.StatusCode)
	}

	res, err := c.Get(srv.URL + "/p/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("old slug answered %d, want 301 — a moved URL that 404s is a dead link", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/p/moving-day" {
		t.Errorf("Location = %q, want /p/moving-day", got)
	}

	// And the new slug is a real page, not another redirect.
	res2, err := c.Get(srv.URL + "/p/moving-day")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("new slug answered %d, want 200", res2.StatusCode)
	}
}

// A slug that never existed still 404s. Without this, "everything is a
// redirect" would pass the test above.
func TestUnknownSlugStill404s(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	res, err := c.Get(srv.URL + "/p/never-existed")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown slug answered %d, want 404", res.StatusCode)
	}
}

// The htmx arm is IDENTICAL for both redirect kinds: HX-Redirect and
// 200. Permanence is a statement to crawlers and caches about a URL,
// and a client-side navigation is neither — a 301 htmx cannot follow
// would be a regression dressed up as correctness.
func TestPermanentRedirectIsStillAnHXRedirectUnderHTMX(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "ada")

	for _, path := range []string{"/redirects", "/moved"} {
		res := htmxPost(t, c, srv.URL+path, url.Values{}, token)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s under htmx answered %d, want 200", path, res.StatusCode)
		}
		if res.Header.Get("HX-Redirect") == "" {
			t.Errorf("%s under htmx sent no HX-Redirect", path)
		}
	}
}

// Plain requests get the two statuses apart. They differ only in a
// number, so nothing else in the stack would notice if they merged.
func TestRedirectAndRedirectPermanentDifferOnPlainRequests(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "ada")

	want := map[string]int{"/redirects": http.StatusSeeOther, "/moved": http.StatusMovedPermanently}
	for path, code := range want {
		res, err := c.PostForm(srv.URL+path, url.Values{"_csrf": {token}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != code {
			t.Errorf("%s answered %d, want %d", path, res.StatusCode, code)
		}
	}
}

// The home page reports a total that is not the size of its page.
//
// Count ignoring Limit is the whole decision, and the only way to see it
// is with more posts than a page holds — so this writes past pageSize.
// Without Count the page could say "here are 50" and never "of 60"; the
// alternative was reading every matching post to render one number.
func TestHomeCountsEveryPostNotJustThePage(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "ada")

	const posts = pageSize + 10
	for i := 0; i < posts; i++ {
		res, err := c.PostForm(srv.URL+"/p", url.Values{
			"title": {fmt.Sprintf("Post %d", i)}, "body": {"body"}, "_csrf": {token}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}

	res, err := c.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	want := fmt.Sprintf("Showing %d of %d posts.", pageSize, posts)
	if !strings.Contains(string(body), want) {
		t.Fatalf("home does not report the total — want %q", want)
	}
}

// A filtered count counts the filter, not the kind. An unpublished post
// is in the store and must not be in the number.
func TestHomeCountRespectsTheFilter(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "ada")

	res, err := c.PostForm(srv.URL+"/p", url.Values{
		"title": {"Draft"}, "body": {"body"}, "_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	id := strings.TrimPrefix(res.Header.Get("Location"), "/p/")

	// Unpublish it: the filter is Published = true.
	un, err := c.PostForm(srv.URL+"/p/"+id+"/publish", url.Values{"_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	un.Body.Close()

	home, err := c.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer home.Body.Close()
	body, _ := io.ReadAll(home.Body)
	if strings.Contains(string(body), "Showing") {
		t.Fatalf("home counted an unpublished post:\n%s", body)
	}
}

// Both ways a request can miss now wear the site.
//
// They are two entirely different paths — a handler returning
// tako.NotFound(), and the site handler finding no file — and that is
// exactly why this is a SiteConfig field. Nothing a handler RETURNS
// could have answered the second one: nothing ran.
func TestBoth404PathsWearTheSite(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)

	for name, path := range map[string]string{
		"a handler's tako.NotFound()": "/p/never-existed",
		"a static miss":               "/no/such/page",
	} {
		res, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %s answered %d, want 404", name, path, res.StatusCode)
		}
		if !strings.Contains(string(body), "Back to the journal") {
			t.Errorf("%s: %s answered plain text, not the site's 404:\n%s", name, path, body)
		}
		// It must be the LAYOUT, not the bare template — the whole
		// point is that a 404 looks like the site.
		if !strings.Contains(string(body), "<html") {
			t.Errorf("%s: 404 body is not a document:\n%s", name, body)
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: content-type = %q", name, ct)
		}
	}
}

// An htmx request gets the rendered document like everyone else. One
// behaviour, no mode switch: a 404 is a 404, and a partial that says
// "not found" swapped into a page is a worse answer than a page.
func TestNotFoundIsTheSameUnderHTMX(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)

	req, err := http.NewRequest("GET", srv.URL+"/p/never-existed", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("htmx 404 answered %d", res.StatusCode)
	}
	if !strings.Contains(string(body), "Back to the journal") {
		t.Errorf("htmx 404 is not the site's page:\n%s", body)
	}
}

// A 500 wears the site too, and says nothing about what broke.
//
// This test used to assert the opposite — that a 500 stayed plain text,
// on the reasoning that the template engine is the worst thing to
// depend on when this process is already broken. FL11 kept the
// reasoning and moved the guarantee: renderStatus recovers a panicking
// or failing status page and falls back to plain text, so the engine is
// tried and never trusted. That fallback is pinned in
// pkg/runtime/status_test.go, where a status page that panics belongs;
// what an EXAMPLE owes is the other half — that the declared page is
// actually reached, and that reaching it leaks nothing.
func TestServerErrorsWearTheSiteAndSayNothing(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	res, err := c.Get(srv.URL + "/boom")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("/boom answered %d, want 500", res.StatusCode)
	}
	if !strings.Contains(string(body), "Something broke") {
		t.Errorf("a 500 did not render the app's status page:\n%s", body)
	}
	if !strings.Contains(string(body), "Back to the journal") {
		t.Errorf("a 500 dropped the site's chrome:\n%s", body)
	}
	// The handler's error text is for the log, under the request id.
	if strings.Contains(string(body), "something came apart") {
		t.Errorf("the 500 page leaked the handler's error:\n%s", body)
	}
	if !strings.Contains(string(body), "Request ") {
		t.Errorf("the 500 page carries no request id to look up:\n%s", body)
	}
}

// The FL5 regression, and the reason this example declares a
// two-segment wildcard page at all.
//
// tako.Auth injects a session endpoint and this app declares
// GET /{year}/{slug}. Before FL5 that pair could not be registered on
// one mux: the endpoint was /api/_session, two segments like the page
// and method-less where the page was GET, so neither was more specific
// on both axes and Go panicked at registration — the service died
// before serving a request. Nothing at request level can catch that;
// this asserts the shape.
func TestAuthAndAWildcardPageCanBeMountedTogether(t *testing.T) {
	// The pre-FL5 mount, kept as the record of what was fixed. If this
	// ever stops panicking, Go's conflict rule changed and the reasoning
	// in internal/routes needs rereading.
	historical := func() (panicked bool) {
		defer func() { panicked = recover() != nil }()
		mux := http.NewServeMux()
		mux.Handle("GET /{year}/{slug}", http.NotFoundHandler())
		mux.Handle("/api/_session", http.NotFoundHandler())
		return false
	}()
	if !historical {
		t.Error("the pre-FL5 pair no longer panics — Go's ServeMux conflict rule has changed")
	}

	// Depth alone would have rescued THIS app and not the next one:
	// /_tako/api/session is three segments, so it clears a two-segment
	// wildcard by being more specific on path. Against a three-segment
	// page — gallery's GET /{user}/{album}/{photo} — the same mount
	// conflicts again. That is why the fix is a separate mux and not a
	// deeper path.
	deeper := func() (panicked bool) {
		defer func() { panicked = recover() != nil }()
		mux := http.NewServeMux()
		mux.Handle("GET /{a}/{b}/{c}", http.NotFoundHandler())
		mux.Handle("/_tako/api/session", http.NotFoundHandler())
		return false
	}()
	if !deeper {
		t.Error("a three-segment wildcard no longer conflicts with the session mount — " +
			"if that is so, the two-mux rationale needs rereading")
	}

	// What journal actually runs: tako's endpoints on their own mux,
	// every declared route on the app's, registered together.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("journal cannot boot: %v", r)
		}
	}()
	takoMux := http.NewServeMux()
	takoMux.Handle("/_tako/api/session", http.NotFoundHandler())
	takoMux.Handle("GET /_tako/signin", http.NotFoundHandler())
	app := http.NewServeMux()
	app.Handle("GET /{year}/{slug}", http.NotFoundHandler())
	app.Handle("GET /p/{id}", http.NotFoundHandler())
	app.Handle("/", http.NotFoundHandler())
	runtime.Reserved(takoMux, app)
}

// The permalink serves the post, and the short URL still does too.
func TestThePermalinkAndTheShortURLBothResolve(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "alice")

	res, err := c.PostForm(srv.URL+"/p", url.Values{
		"title": {"Una nota"}, "body": {"cuerpo"}, "_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	id := strings.TrimPrefix(res.Header.Get("Location"), "/p/")
	if id == "" {
		t.Fatalf("save = %d, no post id in %q", res.StatusCode, res.Header.Get("Location"))
	}

	for _, path := range []string{"/p/" + id, "/2026/" + id} {
		res, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "Una nota") {
			t.Errorf("GET %s → %d, body missing the title", path, res.StatusCode)
		}
	}
}

// The namespace is tako's on the app's own server, not just in a unit
// test: the session endpoint answers, and a path the app never declared
// under /_tako/ is a 404 rather than the site's page.
func TestTheReservedNamespaceAnswersOnTheRealServer(t *testing.T) {
	srv := servePages(t)
	res, err := http.Get(srv.URL + "/_tako/signin?next=/write")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("GET /_tako/signin → %d, want 200", res.StatusCode)
	}

	res2, err := http.Get(srv.URL + "/_tako/z/whatever")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if res2.StatusCode != http.StatusNotFound {
		t.Errorf("GET /_tako/z/whatever → %d, want 404", res2.StatusCode)
	}
	if strings.Contains(string(body), "<!doctype html>") {
		t.Error("the reserved namespace answered with the site's 404 page — it should be tako's own")
	}
}

// A checkbox group reaches the handler whole — the FL15 case.
//
// Every box in the home list is named "selected", which is how HTML
// spells a set. Before []string bindings the Req struct could only
// declare a scalar, so the handler received the FIRST ticked id and
// nothing else: a bulk action that silently acted on one row and
// reported success for all of them. The assertion that matters is the
// count — two ticked, two unpublished — because one-of-two is exactly
// what the old shape produced and it looked like success.
func TestBulkUnpublishReceivesEveryTickedBox(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "alice")

	ids := writePosts(t, c, srv.URL, token, "First", "Second", "Third")

	// Two boxes ticked, one left alone.
	res, err := c.PostForm(srv.URL+"/p/unpublish", url.Values{
		"selected": {ids[0], ids[1]},
		"_csrf":    {token},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Location") != "/" {
		t.Fatalf("bulk unpublish = %d → %q, want 303 → /", res.StatusCode, res.Header.Get("Location"))
	}

	body := getBody(t, c, srv.URL+"/")
	for _, id := range ids[:2] {
		if strings.Contains(body, "/p/"+id) {
			t.Errorf("post %s is still listed after being unpublished:\n%s", id, body)
		}
	}
	// The unticked one is untouched — a bulk action is not a sweep.
	if !strings.Contains(body, "/p/"+ids[2]) {
		t.Errorf("the unticked post was unpublished too:\n%s", body)
	}
	if !strings.Contains(body, "Showing 1 of 1 posts") {
		t.Errorf("want 1 published post left, got:\n%s", body)
	}
}

// Nothing ticked sends no field at all, which is the shape a scalar
// binding and a slice binding disagree about: "" versus nil. The
// handler must see an empty selection, not one empty id.
func TestBulkUnpublishWithNothingTickedChangesNothing(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "alice")
	writePosts(t, c, srv.URL, token, "Only post")

	res, err := c.PostForm(srv.URL+"/p/unpublish", url.Values{"_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 {
		t.Fatalf("empty bulk unpublish = %d", res.StatusCode)
	}
	if body := getBody(t, c, srv.URL+"/"); !strings.Contains(body, "Only post") {
		t.Errorf("an empty selection unpublished something:\n%s", body)
	}
}

// A ticked box for someone else's post is skipped, not obeyed. The
// selection is a request from the browser, so ownership is checked per
// row rather than assumed from the fact that the list rendered.
func TestBulkUnpublishSkipsPostsTheCallerDoesNotOwn(t *testing.T) {
	srv := servePages(t)
	alice := client(t, srv)
	tokenA := signIn(t, alice, srv.URL, "alice")
	hers := writePosts(t, alice, srv.URL, tokenA, "A post of alice's")

	bob := freshClient(t)
	tokenB := signIn(t, bob, srv.URL, "bob")

	res, err := bob.PostForm(srv.URL+"/p/unpublish", url.Values{
		"selected": {hers[0]}, "_csrf": {tokenB}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 {
		t.Fatalf("bulk unpublish = %d", res.StatusCode)
	}
	// Asserted on the id, not the title: the title round-trips through
	// HTML escaping and an apostrophe would make this pass or fail for
	// the wrong reason.
	if body := getBody(t, alice, srv.URL+"/"); !strings.Contains(body, "/p/"+hers[0]) {
		t.Errorf("bob unpublished a post he does not own:\n%s", body)
	}
}

// The home form is the no-JS baseline: boxes are markup, the submit is
// a submit. If the checkboxes ever become JS-built the friction this
// closed is back, so the markup is asserted rather than assumed.
func TestHomeListCarriesPlainCheckboxes(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	token := signIn(t, c, srv.URL, "alice")
	writePosts(t, c, srv.URL, token, "A post")

	body := getBody(t, c, srv.URL+"/")
	if !strings.Contains(body, `<input type="checkbox" name="selected"`) {
		t.Errorf("home has no plain checkbox for bulk selection:\n%s", body)
	}
	if !strings.Contains(body, `action="/p/unpublish"`) {
		t.Errorf("home has no plain form to submit the selection:\n%s", body)
	}
	// The page loads htmx for other features; what matters is that the
	// SELECTION does not reach for it. A form with no hx- attributes
	// submits the same with scripting off.
	form := body[strings.Index(body, `<form method="post" action="/p/unpublish"`):]
	form = form[:strings.Index(form, "</form>")]
	if strings.Contains(form, "hx-") {
		t.Errorf("the selection seam grew an htmx dependency:\n%s", form)
	}
}

// freshClient is a client with its OWN cookie jar. client() hands back
// the server's SHARED client, so two callers of it are one browser —
// fine for one user, wrong for two.
func freshClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := newJar()
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// writePosts publishes each title and returns the ids, read from the
// 303 the save answers with.
func writePosts(t *testing.T, c *http.Client, base, token string, titles ...string) []string {
	t.Helper()
	var ids []string
	for _, title := range titles {
		res, err := c.PostForm(base+"/p", url.Values{
			"title": {title}, "body": {"b"}, "_csrf": {token}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		loc := res.Header.Get("Location")
		if res.StatusCode != 303 || !strings.HasPrefix(loc, "/p/") {
			t.Fatalf("save %q = %d → %q", title, res.StatusCode, loc)
		}
		ids = append(ids, strings.TrimPrefix(loc, "/p/"))
	}
	return ids
}

func getBody(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	res, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("GET %s = %d", url, res.StatusCode)
	}
	return string(body)
}
