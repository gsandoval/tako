package main

import (
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/provision/gcp"
)

// Ids are long and machine-shaped. Asking a human to retype one exactly
// is a way of not being used, so a unique prefix resolves — and an
// ambiguous one says so instead of picking.
func TestPickDeadByPrefix(t *testing.T) {
	msgs := []gcp.DeadMessage{
		{ID: "14672041893"}, {ID: "14672041955"}, {ID: "99900011122"},
	}
	if m, err := pickDead(msgs, "999"); err != nil || m.ID != "99900011122" {
		t.Errorf("unique prefix = %v, %v", m.ID, err)
	}
	if m, err := pickDead(msgs, "14672041893"); err != nil || m.ID != "14672041893" {
		t.Errorf("full id = %v, %v", m.ID, err)
	}
	_, err := pickDead(msgs, "1467")
	if err == nil || !strings.Contains(err.Error(), "matches 2") {
		t.Errorf("ambiguous prefix must refuse and say how many, got %v", err)
	}
	_, err = pickDead(msgs, "zzz")
	if err == nil || !strings.Contains(err.Error(), "no dead letter") {
		t.Errorf("unknown prefix error = %v", err)
	}
}

// --ack throws a message away, --replay puts it back. Guessing which was
// meant is not an option available to a program.
func TestAckAndReplayAreMutuallyExclusive(t *testing.T) {
	err := runDLQLocalArgs([]string{"--ack", "--replay", "abc"})
	if err == nil || !strings.Contains(err.Error(), "opposite things") {
		t.Errorf("expected the mutual-exclusion refusal, got %v", err)
	}
}

func TestReplayNeedsATarget(t *testing.T) {
	err := runDLQLocalArgs([]string{"--replay"})
	if err == nil || !strings.Contains(err.Error(), "needs an id") {
		t.Errorf("bare --replay must ask for an id or --all, got %v", err)
	}
	// With a carrier present the second word IS an id, so this really is
	// "replay exactly this one" and "replay everything" at once.
	// (`--replay abc --all` with no carrier is not ambiguous: abc is the
	// carrier, and replaying all of it is a coherent request.)
	if err := runDLQLocalArgs([]string{"thumbnails", "--replay", "abc", "--all"}); err == nil ||
		!strings.Contains(err.Error(), "not both") {
		t.Errorf("id AND --all must refuse, got %v", err)
	}
}
