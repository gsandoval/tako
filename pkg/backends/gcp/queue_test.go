package gcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// TestWithDelayNeedsRoute pins the misconfiguration text (T3): a
// delayed publish without the deploy-injected Cloud Tasks route means
// the binary and the deploy disagree — say so, never deliver early.
func TestWithDelayNeedsRoute(t *testing.T) {
	s := NewQueueStore(&Client{Opts: Options{Project: "p", Env: "prod"}})
	err := s.Publish(context.Background(), "q", []byte("{}"), backends.PublishOptions{Delay: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "no Cloud Tasks route") {
		t.Fatalf("WithDelay error = %v", err)
	}
}

// TestTasksRouteParses pins the env route format the deploy injects.
func TestTasksRouteParses(t *testing.T) {
	t.Setenv("TAKO_TASKS_Q", "projects/p/locations/r/queues/q-tako|https://w.example/_tako/api/push|w-sa@p.iam.gserviceaccount.com")
	s := NewQueueStore(&Client{Opts: Options{Project: "p", Env: "prod"}})
	// The enqueue itself would hit the network; a malformed route must
	// fail BEFORE that with its own text.
	t.Setenv("TAKO_TASKS_Q", "only-one-part")
	err := s.Publish(context.Background(), "q", []byte("{}"), backends.PublishOptions{})
	if err == nil || !strings.Contains(err.Error(), "malformed TAKO_TASKS route") {
		t.Fatalf("malformed route error = %v", err)
	}
}

// TestDeadLetterNeedsService pins the misconfiguration text: Permanent
// short-circuits derive the dead topic from TAKO_SERVICE.
func TestDeadLetterNeedsService(t *testing.T) {
	c := &Client{Opts: Options{Project: "p", Env: "prod"}}
	err := c.DeadLetter(context.Background(), []byte("{}"), "boom")
	if err == nil || !strings.Contains(err.Error(), "TAKO_SERVICE") {
		t.Fatalf("DeadLetter without service = %v", err)
	}
}
