package local

import (
	"context"
	"errors"
	"sync"

	"github.com/gsandoval/tako/pkg/backends"
)

// PushStore implements backends.Push in-memory for local development and
// NewTestApp assertions.
type PushStore struct {
	mu          sync.Mutex
	messages    []backends.PushMessage
	unregTokens map[string]bool
}

// NewPushStore creates an in-memory PushStore.
func NewPushStore() *PushStore {
	return &PushStore{
		unregTokens: map[string]bool{},
	}
}

// MarkUnregistered marks token as unregistered so Send returns
// backends.ErrUnregisteredPushToken in tests.
func (s *PushStore) MarkUnregistered(token string) {
	s.mu.Lock()
	s.unregTokens[token] = true
	s.mu.Unlock()
}

// Send records msg for channel after validating that either Token or Topic
// is provided.
func (s *PushStore) Send(_ context.Context, channel string, msg backends.PushMessage) error {
	if (msg.Token == "" && msg.Topic == "") || (msg.Token != "" && msg.Topic != "") {
		return errors.New("tako: PushMessage must set exactly one of Token or Topic")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if msg.Token != "" && s.unregTokens[msg.Token] {
		return backends.ErrUnregisteredPushToken
	}
	cloned := msg.Clone()
	cloned.Channel = channel
	s.messages = append(s.messages, cloned)
	return nil
}

// Messages returns all push messages sent on channel (or all channels when
// channel is empty).
func (s *PushStore) Messages(channel string) []backends.PushMessage {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []backends.PushMessage
	for _, m := range s.messages {
		if channel == "" || m.Channel == channel {
			out = append(out, m.Clone())
		}
	}
	return out
}
