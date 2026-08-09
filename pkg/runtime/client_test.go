package runtime

// AB3a: who sent the request.
//
// The case that matters is the third one. X-Forwarded-For is a list the
// caller can start, and Google's frontend appends to it rather than
// replacing it — so the left-most entry is whatever the caller wanted to
// look like. An app that rate-limits on it has a rate limit with an
// opt-out, which is worse than the site-wide cap it replaced.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	tako "github.com/gsandoval/tako"
)

func TestTheClientIPIsReadPastTheHopsTakoPutsThere(t *testing.T) {
	for _, c := range []struct {
		name string
		xff  []string
		want string
	}{
		{
			// Cloud Run, nobody spoofing: <client>, <frontend>.
			name: "behind the frontend",
			xff:  []string{"203.0.113.7, 130.211.0.1"},
			want: "203.0.113.7",
		},
		{
			// The whole point. The caller sent its own header, so the
			// list is <forged>, <real client>, <frontend> — and reading
			// from the left would hand an app the forgery.
			name: "a caller that sent its own header",
			xff:  []string{"1.2.3.4, 203.0.113.7, 130.211.0.1"},
			want: "203.0.113.7",
		},
		{
			// Repeated headers are one list, which is how a forger with
			// a header-splitting client would try it instead.
			name: "spread across two headers",
			xff:  []string{"1.2.3.4", "203.0.113.7, 130.211.0.1"},
			want: "203.0.113.7",
		},
		{
			// Shorter than the hops tako expects: it did not come
			// through them, so it says nothing and the socket wins.
			name: "a bare forged header, no proxy",
			xff:  []string{"1.2.3.4"},
			want: "192.0.2.1",
		},
		{
			name: "no header at all",
			xff:  nil,
			want: "192.0.2.1",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = "192.0.2.1:54321" // httptest's default socket
			for _, v := range c.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := clientIP(r); got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTheClientReachesTheHandler(t *testing.T) {
	var seen tako.Client
	page := tako.Page("GET /who", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		seen = ctx.Client()
		return tako.HTML{}, nil
	})

	r := httptest.NewRequest("GET", "/who", nil)
	r.RemoteAddr = "192.0.2.1:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 130.211.0.1")
	r.Header.Set("User-Agent", "curl/8.6.0")
	PageHandler(page, nil).ServeHTTP(httptest.NewRecorder(), r)

	if seen.IP != "203.0.113.7" {
		t.Errorf("IP = %q", seen.IP)
	}
	if seen.UserAgent != "curl/8.6.0" {
		t.Errorf("UserAgent = %q", seen.UserAgent)
	}
}

// A queue message has no client, and inventing the publisher's would be
// worse than saying nothing. The zero value is the honest answer, and it
// is what an async handler gets.
func TestAnAsyncHandlerHasNoClient(t *testing.T) {
	ctx := &requestCtx{Context: t.Context(), log: requestLogger(httptest.NewRequest("GET", "/", nil), "test")}
	if got := ctx.Client(); got != (tako.Client{}) {
		t.Fatalf("Client() = %+v, want the zero value", got)
	}
}

// And the JSON path answers the same as the page path — four handlers
// build a request Ctx, and a client that reached only some of them would
// be a fact that depends on which kind of route you happened to declare.
func TestEveryRequestPathCarriesTheClient(t *testing.T) {
	api := tako.API("GET /whoapi", func(ctx tako.Ctx, _ struct{}) (string, error) {
		return ctx.Client().IP, nil
	})
	r := httptest.NewRequest("GET", "/whoapi", nil)
	r.RemoteAddr = "192.0.2.1:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 130.211.0.1")
	w := httptest.NewRecorder()
	JSONHandler(api, nil).ServeHTTP(w, r)

	if got := w.Body.String(); got != "\"203.0.113.7\"\n" {
		t.Fatalf("body = %q", got)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}
