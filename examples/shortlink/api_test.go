package shortlink

// AB3a: a click remembers who followed the link.
//
// The worker cannot ask who that was — an async handler has no client,
// and inventing the publisher's would be a lie — so the fact is captured
// at the REQUEST and travels in the message. That split is the whole
// shape of ctx.Client() in an app with workers.
//
// It is also the first test here to drive the app over HTTP, because a
// header is a request fact and Endpoint.Call has no request:
// app.Handler() (AB2) is what makes one available.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsandoval/tako"
)

func TestAClickRemembersWhoFollowedIt(t *testing.T) {
	app := tako.NewTestApp(t)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	ctx := app.Ctx(app.SignIn("memo"))
	if _, err := Create.Call(ctx, CreateReq{Slug: "gh", URL: "https://github.com"}); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("GET", srv.URL+"/api/links/gh", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The header as Cloud Run presents it — the caller, then Google's
	// frontend — with a forged entry in front of both, which is what a
	// caller can always put there.
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.7, 130.211.0.1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("resolve = %d", res.StatusCode)
	}
	app.DrainQueues()

	clicks, err := seen.Query(app.Ctx()).All()
	if err != nil {
		t.Fatal(err)
	}
	if len(clicks) != 1 {
		t.Fatalf("counted %d clicks, want 1", len(clicks))
	}
	// Not 1.2.3.4: the left-most entry is whatever the caller wanted to
	// look like, and a click log that recorded it would be a click log
	// anybody can write.
	if clicks[0].From != "203.0.113.7" {
		t.Fatalf("click came from %q, want 203.0.113.7", clicks[0].From)
	}
}
