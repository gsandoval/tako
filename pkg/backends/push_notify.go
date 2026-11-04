package backends

import (
	"context"
	"errors"
	"maps"
)

// ErrUnregisteredPushToken is returned when FCM reports that a device
// push token is no longer registered (HTTP 404 UNREGISTERED) so the caller
// can evict the token from the user/device profile entity.
var ErrUnregisteredPushToken = errors.New("tako: unregistered push token")

// PushMessage is a mobile push notification payload sent via tako.Push.
type PushMessage struct {
	Channel string            `json:"channel,omitempty"`
	Token   string            `json:"token,omitempty"`
	Topic   string            `json:"topic,omitempty"`
	Title   string            `json:"title,omitempty"`
	Body    string            `json:"body,omitempty"`
	Data    map[string]string `json:"data,omitempty"`
}

// Clone returns a deep copy of m.
func (m PushMessage) Clone() PushMessage {
	if len(m.Data) > 0 {
		d := make(map[string]string, len(m.Data))
		maps.Copy(d, m.Data)
		m.Data = d
	}
	return m
}

// Push is the backend contract for sending mobile push notifications.
type Push interface {
	Send(ctx context.Context, channel string, msg PushMessage) error
}
