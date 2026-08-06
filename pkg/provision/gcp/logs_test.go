package gcp

import "testing"

// The gate walk found `tako logs` printing BLANK lines: it has no
// logName filter, so it ingests Cloud Run's own request entries, whose
// content lives in httpRequest rather than in any payload — and nothing
// read it. Now those render like the dev console's line.
func TestRequestLineRendersCloudRunsOwnEntry(t *testing.T) {
	got := requestLine("POST", "https://logging-gallery-abc-uc.a.run.app/api/photos", 200, "0.191464485s")
	if got != "POST /api/photos → 200 (191.5ms)" {
		t.Errorf("requestLine = %q", got)
	}
	// A path-only URL (tako's own older lines, and some platforms) must
	// not be mangled by the host-stripping.
	if got := requestLine("GET", "/api/photos/nope/image", 404, "0.033743s"); got != "GET /api/photos/nope/image → 404 (33.7ms)" {
		t.Errorf("requestLine = %q", got)
	}
	// No latency is not a reason to print a broken suffix.
	if got := requestLine("POST", "/_tako/api/push", 403, "0s"); got != "POST /_tako/api/push → 403" {
		t.Errorf("requestLine = %q", got)
	}
	if got := requestLine("GET", "/x", 200, ""); got != "GET /x → 200" {
		t.Errorf("requestLine with no latency = %q", got)
	}
}

// RC2. The reader used to print jsonPayload["message"] and drop the
// rest, so a handler error read "handler error" and the err — the
// field naming the actual failure — never reached the terminal. This is
// the exact payload examples/gallery wrote when its derived index set
// was missing a shape (RC1); diagnosing it needed gcloud, which is the
// bug.
func TestHandlerErrorShowsTheErrorAndTheRequestID(t *testing.T) {
	got := renderPayload(map[string]any{
		"message": "handler error",
		"err":     "HTTP 400 from /v1/projects/tako-test-01:runQuery: no matching index found.",
		"req":     "759268",
	})
	want := "handler error [req=759268]\n" +
		"err: HTTP 400 from /v1/projects/tako-test-01:runQuery: no matching index found."
	if got != want {
		t.Errorf("payload:\n got: %q\nwant: %q", got, want)
	}
}

// Datastore's recommendation is a YAML block, and folding it onto one
// line is what makes it unreadable — the shape IS the remediation.
func TestAMultiLineErrorKeepsItsShape(t *testing.T) {
	got := renderPayload(map[string]any{
		"message": "handler error",
		"err":     "no matching index found. recommended index is:\n- kind: photos\n  properties:\n  - name: ready\n",
	})
	want := "handler error\n" +
		"err: no matching index found. recommended index is:\n" +
		"     - kind: photos\n" +
		"       properties:\n" +
		"       - name: ready"
	if got != want {
		t.Errorf("payload:\n got: %q\nwant: %q", got, want)
	}
}

// A payload shape this command has not met degrades readably. The old
// fallback was fmt.Sprint over the map — Go syntax in random order.
func TestAnUnknownPayloadDegradesReadably(t *testing.T) {
	got := renderPayload(map[string]any{
		"message": "thumbnail written",
		"bytes":   4096,
		"key":     "photos/abc.jpg",
	})
	want := "thumbnail written\nbytes: 4096\nkey: photos/abc.jpg"
	if got != want {
		t.Errorf("payload:\n got: %q\nwant: %q", got, want)
	}
}

// Cloud Logging's own keys are the entry's columns already, or noise.
func TestLoggingsOwnKeysAreNotEchoed(t *testing.T) {
	got := renderPayload(map[string]any{
		"message":                      "cron ok",
		"severity":                     "INFO",
		"time":                         "2026-08-05T18:11:46Z",
		"logging.googleapis.com/trace": "projects/p/traces/abc",
	})
	if got != "cron ok" {
		t.Errorf("payload = %q, want %q", got, "cron ok")
	}
}
