package hello

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gsandoval/tako/pkg/runtime"
)

// TestHello hits the endpoint over HTTP the way the generated service
// serves it. (tako.NewTestApp replaces this plumbing in a later slice.)
func TestHello(t *testing.T) {
	srv := httptest.NewServer(runtime.JSONHandler(Hello))
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q, want application/json", ct)
	}
	var got Greeting
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Message != "Hello from Tako!" {
		t.Fatalf("message = %q", got.Message)
	}
}
