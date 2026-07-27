package shop

// The W7 contract: a JSON api behind session auth, consumed by a
// GENERATED TypeScript client. The Go tests pin the server half —
// 401s, ownership scoping, error texts, and the committed client's
// freshness; the client itself typechecks (and drives the deployed
// api) in the W7 validation script.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/clientgen"
	"github.com/gsandoval/tako/pkg/runtime"
)

// serveAPI mirrors the generated main: session routes + JSONHandler
// per endpoint, decode functions written the way codegen writes them.
func serveAPI(t *testing.T) *httptest.Server {
	t.Helper()
	if err := runtime.BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/_session", runtime.SessionHandler())
	mux.Handle("GET /api/products", runtime.JSONHandler(ListProducts, nil))
	mux.Handle("GET /api/products/{id}", runtime.JSONHandler(GetProduct, func(r *http.Request) (GetProductReq, error) {
		return GetProductReq{ID: runtime.PathString(r, "id")}, nil
	}))
	mux.Handle("POST /api/products", runtime.JSONHandler(AddProduct, func(r *http.Request) (AddProductReq, error) {
		var req AddProductReq
		err := runtime.DecodeJSON(r, &req)
		return req, err
	}, runtime.RequireAuth()))
	mux.Handle("POST /api/orders", runtime.JSONHandler(PlaceOrder, func(r *http.Request) (PlaceOrderReq, error) {
		var req PlaceOrderReq
		err := runtime.DecodeJSON(r, &req)
		return req, err
	}, runtime.RequireAuth()))
	mux.Handle("GET /api/orders", runtime.JSONHandler(ListOrders, nil, runtime.RequireAuth()))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// signIn mints a dev session (the local backend's path) on a
// cookie-jarred client.
func signIn(t *testing.T, srv *httptest.Server, name string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// The dev sign-in 303s to "/" — an API-only harness has no home, so
	// judge the redirect itself instead of following it.
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := c.PostForm(srv.URL+"/api/_session", url.Values{"dev_user": {name}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 303 {
		t.Fatalf("dev sign-in = %d", res.StatusCode)
	}
	return c
}

func postJSON[Res any](t *testing.T, c *http.Client, u string, body any) (Res, *http.Response) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Post(u, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out Res
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode == 200 {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decoding %s: %v (%s)", u, err, data)
		}
	}
	res.Body = io.NopCloser(bytes.NewReader(data))
	return out, res
}

func getJSON[Res any](t *testing.T, c *http.Client, u string) (Res, *http.Response) {
	t.Helper()
	res, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out Res
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode == 200 {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decoding %s: %v (%s)", u, err, data)
		}
	}
	res.Body = io.NopCloser(bytes.NewReader(data))
	return out, res
}

func TestCatalogAndOrderFlow(t *testing.T) {
	srv := serveAPI(t)
	alice := signIn(t, srv, "alice")

	tea, res := postJSON[Product](t, alice, srv.URL+"/api/products", AddProductReq{Name: "tea", PriceCents: 450})
	if res.StatusCode != 200 || tea.ID == "" {
		t.Fatalf("add tea = %d %+v", res.StatusCode, tea)
	}
	coffee, _ := postJSON[Product](t, alice, srv.URL+"/api/products", AddProductReq{Name: "coffee", PriceCents: 300})

	list, _ := getJSON[[]Product](t, alice, srv.URL+"/api/products")
	if len(list) != 2 || list[0].Name != "coffee" || list[1].Name != "tea" {
		t.Fatalf("catalog = %+v, want [coffee tea] (sorted by name)", list)
	}

	got, res2 := getJSON[Product](t, alice, srv.URL+"/api/products/"+coffee.ID)
	if res2.StatusCode != 200 || got.PriceCents != 300 {
		t.Fatalf("get coffee = %d %+v", res2.StatusCode, got)
	}

	order, res3 := postJSON[Order](t, alice, srv.URL+"/api/orders", PlaceOrderReq{ProductID: tea.ID, Qty: 3})
	if res3.StatusCode != 200 || order.TotalCents != 1350 || order.Qty != 3 {
		t.Fatalf("order = %d %+v, want total 1350", res3.StatusCode, order)
	}
	if order.ReceiptID == "" {
		t.Fatal("order carries no receipt — the billing.Charge call did not happen")
	}

	mine, _ := getJSON[[]Order](t, alice, srv.URL+"/api/orders")
	if len(mine) != 1 || mine[0].ID != order.ID {
		t.Fatalf("my orders = %+v, want the one just placed", mine)
	}
}

func TestWritesRequireSession(t *testing.T) {
	srv := serveAPI(t)
	anon := &http.Client{}
	for _, probe := range []func() *http.Response{
		func() *http.Response {
			_, res := postJSON[Order](t, anon, srv.URL+"/api/orders", PlaceOrderReq{ProductID: "x", Qty: 1})
			return res
		},
		func() *http.Response {
			_, res := postJSON[Product](t, anon, srv.URL+"/api/products", AddProductReq{Name: "x", PriceCents: 1})
			return res
		},
		func() *http.Response {
			_, res := getJSON[[]Order](t, anon, srv.URL+"/api/orders")
			return res
		},
	} {
		res := probe()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 401 || !strings.Contains(string(body), "sign in to continue") {
			t.Fatalf("anonymous call = %d %q, want 401 sign in to continue", res.StatusCode, body)
		}
	}
}

func TestUnknownProductTexts(t *testing.T) {
	srv := serveAPI(t)
	alice := signIn(t, srv, "alice")

	_, res := getJSON[Product](t, alice, srv.URL+"/api/products/nope")
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 404 || !strings.Contains(string(body), "unknown product") {
		t.Fatalf("get unknown = %d %q", res.StatusCode, body)
	}

	_, res2 := postJSON[Order](t, alice, srv.URL+"/api/orders", PlaceOrderReq{ProductID: "nope", Qty: 1})
	body2, _ := io.ReadAll(res2.Body)
	if res2.StatusCode != 404 || !strings.Contains(string(body2), "unknown product") {
		t.Fatalf("order unknown = %d %q", res2.StatusCode, body2)
	}
}

func TestOrdersAreOwnerScoped(t *testing.T) {
	srv := serveAPI(t)
	alice := signIn(t, srv, "alice")
	p, _ := postJSON[Product](t, alice, srv.URL+"/api/products", AddProductReq{Name: "mug", PriceCents: 900})
	if _, res := postJSON[Order](t, alice, srv.URL+"/api/orders", PlaceOrderReq{ProductID: p.ID, Qty: 1}); res.StatusCode != 200 {
		t.Fatalf("alice's order = %d", res.StatusCode)
	}

	bob := signIn(t, srv, "bob")
	theirs, _ := getJSON[[]Order](t, bob, srv.URL+"/api/orders")
	if len(theirs) != 0 {
		t.Fatalf("bob sees alice's orders: %+v", theirs)
	}
}

func TestValidationTexts(t *testing.T) {
	srv := serveAPI(t)
	alice := signIn(t, srv, "alice")
	cases := []struct {
		call func() *http.Response
		want string
	}{
		{func() *http.Response {
			_, res := postJSON[Product](t, alice, srv.URL+"/api/products", AddProductReq{Name: "", PriceCents: 5})
			return res
		}, "name must not be empty"},
		{func() *http.Response {
			_, res := postJSON[Product](t, alice, srv.URL+"/api/products", AddProductReq{Name: "x", PriceCents: 0})
			return res
		}, "priceCents must be positive"},
		{func() *http.Response {
			_, res := postJSON[Order](t, alice, srv.URL+"/api/orders", PlaceOrderReq{ProductID: "x", Qty: 0})
			return res
		}, "qty must be at least 1"},
	}
	for _, tc := range cases {
		res := tc.call()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 422 || !strings.Contains(string(body), tc.want) {
			t.Fatalf("validation = %d %q, want 422 %q", res.StatusCode, body, tc.want)
		}
	}
}

// TestGeneratedClientIsFresh pins web/src/api.ts to the generator's
// output — the committed client can never drift from the Go
// signatures without this failing.
func TestGeneratedClientIsFresh(t *testing.T) {
	tg, err := analyzer.AnalyzeTyped(analyzer.Config{Dir: "."})
	if err != nil {
		t.Fatal(err)
	}
	content, sum, err := clientgen.Generate(tg)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Endpoints != 5 || len(sum.Excluded) != 1 {
		t.Fatalf("summary = %+v, want 5 endpoints and billing.Charge excluded (private)", sum)
	}
	if !strings.Contains(sum.Excluded[0].Reason, "private") {
		t.Fatalf("exclusion reason = %q, want the private explanation", sum.Excluded[0].Reason)
	}
	committed, err := os.ReadFile("web/src/api.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, committed) {
		t.Fatal("web/src/api.ts is stale — regenerate: tako gen client ts --out web/src/api.ts")
	}
}
