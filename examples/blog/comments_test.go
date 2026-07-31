package blog

// The S2 contract: a Hugo-generated site whose only dynamic surface is
// an htmx island — the fragment renders WITH form and CSRF, posting
// re-renders the island under htmx and 303s back to the static page
// without JS, and only site-local post paths are accepted. The harness
// mirrors the generated main; templates come from site/static (the
// same files Hugo copies into public/), so no Hugo run is needed here.

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

	"github.com/gsandoval/tako/pkg/runtime"
)

func serveFrags(t *testing.T) *httptest.Server {
	t.Helper()
	if err := runtime.BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	runtime.RegisterSiteTemplates(os.DirFS("site/static"), runtime.SiteOptions{})
	mux := http.NewServeMux()
	mux.Handle("GET /frag/comments", runtime.PageHandler(Comments, func(r *http.Request) (ListReq, error) {
		return ListReq{Post: runtime.QueryString(r, "post")}, nil
	}))
	mux.Handle("POST /frag/comments", runtime.PageHandler(AddComment, func(r *http.Request) (AddReq, error) {
		return AddReq{
			Post: runtime.FormString(r, "post"),
			Name: runtime.FormString(r, "name"),
			Body: runtime.FormString(r, "body"),
		}, nil
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func jarClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	c := srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Jar = jar
	return c
}

var tokenRe = regexp.MustCompile(`name="_csrf" value="([0-9a-f]+)"`)

// island GETs one post's fragment and returns its HTML and CSRF token.
func getIsland(t *testing.T, c *http.Client, base, post string) (string, string) {
	t.Helper()
	res, err := c.Get(base + "/frag/comments?post=" + url.QueryEscape(post))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	m := tokenRe.FindSubmatch(body)
	if res.StatusCode != 200 || m == nil {
		t.Fatalf("fragment = %d, csrf input present = %v", res.StatusCode, m != nil)
	}
	return string(body), string(m[1])
}

func TestIslandRoundTrip(t *testing.T) {
	srv := serveFrags(t)
	c := jarClient(t, srv)
	post := "/posts/hello-tako/"

	html, token := getIsland(t, c, srv.URL, post)
	if !strings.Contains(html, "No comments yet") || !strings.Contains(html, `name="post" value="`+post+`"`) {
		t.Fatalf("empty island should carry the empty state and the post binding:\n%s", html)
	}

	// No-JS path: a plain form post lands back on the static page.
	form := url.Values{"post": {post}, "name": {"ada"}, "body": {"first!"}, "_csrf": {token}}
	res, err := c.PostForm(srv.URL+"/frag/comments", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Location") != post {
		t.Fatalf("no-JS post = %d → %q, want 303 → %q (POST-redirect-GET)", res.StatusCode, res.Header.Get("Location"), post)
	}

	// htmx path: the island re-renders in place, comment included.
	form.Set("name", "grace")
	form.Set("body", "second")
	req, _ := http.NewRequest("POST", srv.URL+"/frag/comments", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	hres, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer hres.Body.Close()
	swapped, _ := io.ReadAll(hres.Body)
	if hres.StatusCode != 200 {
		t.Fatalf("htmx post = %d, want 200 (in-place swap)", hres.StatusCode)
	}
	got := string(swapped)
	if !strings.Contains(got, "ada") || !strings.Contains(got, "grace") {
		t.Fatalf("re-rendered island should list both comments in order:\n%s", got)
	}
	if strings.Index(got, "ada") > strings.Index(got, "grace") {
		t.Fatalf("comments should render oldest-first:\n%s", got)
	}

	// Islands are per post: another page starts empty.
	other, _ := getIsland(t, c, srv.URL, "/posts/going-static/")
	if !strings.Contains(other, "No comments yet") {
		t.Fatalf("comments must not leak across posts:\n%s", other)
	}
}

func TestCSRFAndPathJudgments(t *testing.T) {
	srv := serveFrags(t)
	c := jarClient(t, srv)
	post := "/posts/hello-tako/"
	_, token := getIsland(t, c, srv.URL, post)

	// A post without the token is the shim's 403, not the handler's.
	res, err := c.PostForm(srv.URL+"/frag/comments", url.Values{"post": {post}, "name": {"x"}, "body": {"y"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 403 || !strings.Contains(string(body), "CSRF") {
		t.Fatalf("missing token = %d %q, want the 403 CSRF text", res.StatusCode, body)
	}

	// The post binding is a site-local path, never a redirect target.
	res2, err := c.PostForm(srv.URL+"/frag/comments",
		url.Values{"post": {"https://evil.example/"}, "name": {"x"}, "body": {"y"}, "_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if res2.StatusCode != 422 || !strings.Contains(string(body2), "site-local") {
		t.Fatalf("foreign post = %d %q, want 422 naming site-local paths", res2.StatusCode, body2)
	}

	// Empty fields are the handler's 422.
	res3, err := c.PostForm(srv.URL+"/frag/comments",
		url.Values{"post": {post}, "name": {" "}, "body": {"y"}, "_csrf": {token}})
	if err != nil {
		t.Fatal(err)
	}
	body3, _ := io.ReadAll(res3.Body)
	res3.Body.Close()
	if res3.StatusCode != 422 || !strings.Contains(string(body3), "name and a body") {
		t.Fatalf("empty name = %d %q, want the 422 text", res3.StatusCode, body3)
	}
}
