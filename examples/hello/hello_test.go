package hello

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsandoval/tako"
	"github.com/gsandoval/tako/pkg/runtime"
)

// serve mirrors the generated service's mux, including the generated
// decode logic, so tests exercise the HTTP surface a client sees.
// (tako.NewTestApp replaces this plumbing in F7.)
func serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /hello", runtime.JSONHandler(Hello, nil))
	mux.Handle("GET /greet/{name}", runtime.JSONHandler(Greet, func(r *http.Request) (GreetReq, error) {
		var req GreetReq
		var err error
		req.Name = runtime.PathString(r, "name")
		if req.Loud, err = runtime.QueryBool(r, "loud"); err != nil {
			return req, err
		}
		return req, nil
	}))
	mux.Handle("POST /echo", runtime.JSONHandler(Echo, func(r *http.Request) (EchoReq, error) {
		var req EchoReq
		if err := runtime.DecodeJSON(r, &req); err != nil {
			return req, err
		}
		return req, nil
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestEndpoints(t *testing.T) {
	srv := serve(t)
	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{"hello", "GET", "/hello", "", 200, `{"message":"Hello from Tako!"}`},
		{"greet", "GET", "/greet/memo", "", 200, `{"message":"Hello, memo!"}`},
		{"greet loud", "GET", "/greet/memo?loud=true", "", 200, `{"message":"HELLO, MEMO!"}`},
		{"greet not found", "GET", "/greet/nobody", "", 404, `{"error":"not found"}`},
		{"bad query", "GET", "/greet/memo?loud=banana", "", 400, `{"error":"query \"loud\": \"banana\" is not a bool"}`},
		{"echo", "POST", "/echo", `{"message":"hi"}`, 200, `{"message":"hi"}`},
		{"echo bad json", "POST", "/echo", `{oops`, 400, `{"error":"body: invalid JSON"}`},
		{"echo empty", "POST", "/echo", `{"message":""}`, 422, `{"error":"message must not be empty"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			res, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", res.StatusCode, tc.wantStatus)
			}
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(body)); got != tc.wantBody {
				t.Fatalf("body = %s, want %s", got, tc.wantBody)
			}
		})
	}
}

// TestCall exercises the in-process call path handlers use in tests.
func TestCall(t *testing.T) {
	ctx := runtime.NewCtx(t.Context())

	got, err := Greet.Call(ctx, GreetReq{Name: "memo", Loud: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Message != "HELLO, MEMO!" {
		t.Fatalf("message = %q", got.Message)
	}

	_, err = Greet.Call(ctx, GreetReq{Name: "nobody"})
	if !tako.IsNotFound(err) {
		t.Fatalf("err = %v, want IsNotFound", err)
	}
}
