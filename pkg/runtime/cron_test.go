package runtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The dispatch route trusts nobody by default: no bearer → 401; a
// token on a service with no injected trigger identity (local dev,
// misconfiguration) → 403 with the configured-identity text. The
// happy path needs Google-signed tokens and runs in the T2 gate.
func TestCronDispatchRefusesStrangers(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("POST /_tako/cron/{cron}", CronDispatch())
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/_tako/cron/Cleanup", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("no token = %d, want 401", res.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/_tako/cron/Cleanup", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	body := make([]byte, 512)
	n, _ := res2.Body.Read(body)
	if res2.StatusCode != 403 || !strings.Contains(string(body[:n]), "no cron trigger identity configured") {
		t.Fatalf("bad token = %d %q, want 403 naming the missing identity", res2.StatusCode, body[:n])
	}
}
