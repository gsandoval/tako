package runtime

import "testing"

// A topic fans out; a dead letter belongs to one subscriber. The replay
// attribute is how the cloud path addresses it, and everyone else has
// to ack and do nothing — otherwise replaying re-runs the subscribers
// that already succeeded.
func TestReplayAddressing(t *testing.T) {
	t.Setenv("TAKO_SERVICE", "gallery-notifyowner")

	for _, tc := range []struct {
		name      string
		attrs     map[string]string
		elsewhere bool
	}{
		{"normal delivery has no replay attribute", map[string]string{"tako-topic": "photo-ready"}, false},
		{"no attributes at all", nil, false},
		{"addressed to us", map[string]string{ReplayAttr: "gallery-notifyowner"}, false},
		{"addressed to a sibling", map[string]string{ReplayAttr: "gallery-warmfeed"}, true},
	} {
		if _, got := addressedElsewhere(tc.attrs); got != tc.elsewhere {
			t.Errorf("%s: elsewhere = %v, want %v", tc.name, got, tc.elsewhere)
		}
	}
}
