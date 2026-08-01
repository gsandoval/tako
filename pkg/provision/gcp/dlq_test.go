package gcp

import "testing"

// Replay has to know where to put a message back. Pub/Sub's dead-letter
// forwarding preserves the ORIGINAL attributes, so the carrier that
// published it is still named on the message that failed.
func TestDeadMessageSource(t *testing.T) {
	for _, tc := range []struct {
		name       string
		attrs      map[string]string
		kind, want string
		ok         bool
	}{
		{"queue", map[string]string{"tako-queue": "thumbnails"}, "queue", "thumbnails", true},
		{"topic", map[string]string{"tako-topic": "photo-ready"}, "topic", "photo-ready", true},
		{"dead stamps alone are not a source",
			map[string]string{"tako-dead-reason": "boom", "tako-service": "g-makethumb"}, "", "", false},
		{"nothing", nil, "", "", false},
	} {
		kind, name, ok := DeadMessage{Attributes: tc.attrs}.Source()
		if kind != tc.kind || name != tc.want || ok != tc.ok {
			t.Errorf("%s: (%q,%q,%v), want (%q,%q,%v)", tc.name, kind, name, ok, tc.kind, tc.want, tc.ok)
		}
	}
}
