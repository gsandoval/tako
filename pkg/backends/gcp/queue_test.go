package gcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// TestWithDelayErrorsOnGCP pins the not-yet text: Pub/Sub cannot delay,
// so erroring beats silently delivering early. Cloud Tasks waits for
// the example that demands WithDelay in deployed code (C5 scope call).
func TestWithDelayErrorsOnGCP(t *testing.T) {
	s := NewQueueStore(&Client{Opts: Options{Project: "p", Env: "prod"}})
	err := s.Publish(context.Background(), "q", []byte("{}"), backends.PublishOptions{Delay: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "Cloud Tasks — that arrives with the delayed-delivery feature") {
		t.Fatalf("WithDelay error = %v", err)
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
