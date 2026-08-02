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
	if got := requestLine("POST", "/_tako/push", 403, "0s"); got != "POST /_tako/push → 403" {
		t.Errorf("requestLine = %q", got)
	}
	if got := requestLine("GET", "/x", 200, ""); got != "GET /x → 200" {
		t.Errorf("requestLine with no latency = %q", got)
	}
}
