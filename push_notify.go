package tako

import (
	"errors"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/trace"
	"github.com/gsandoval/tako/pkg/backends"
)

// Push declares a mobile push notification channel backed by Firebase
// Cloud Messaging (FCM HTTP v1 API) in the cloud and an inspectable
// in-memory store in local dev and NewTestApp.
func Push(name string) *PushHandle {
	return &PushHandle{name: name}
}

// PushHandle is a declared mobile push notification channel.
type PushHandle struct {
	name string
}

// Name returns the declared name of the push channel.
func (p *PushHandle) Name() string { return p.name }

// PushMessage is a mobile push notification sent to a single device Token
// or broadcast to an FCM Topic.
type PushMessage struct {
	// Token targets a specific mobile device registration token.
	// Exactly one of Token or Topic must be set.
	Token string `json:"token,omitempty"`
	// Topic targets an FCM topic name.
	Topic string `json:"topic,omitempty"`
	// Title is the notification title shown on the device.
	Title string `json:"title,omitempty"`
	// Body is the notification body text shown on the device.
	Body string `json:"body,omitempty"`
	// Data is arbitrary key-value payload delivered to the client app.
	Data map[string]string `json:"data,omitempty"`
}

// Send dispatches msg on this push channel. Use IsUnregisteredPushToken(err)
// to detect stale device tokens that should be evicted from user/device
// profile entities.
func (p *PushHandle) Send(ctx Ctx, msg PushMessage) error {
	sctx, finish := trace.StartSpan(ctx, "push", p.name+".Send", "push", p.name, "op", "Send")
	var store backends.Push
	if a := appFrom(ctx); a != nil && a.Push != nil {
		store = a.Push
	} else {
		store = hooks.Push
	}
	if store == nil {
		panic(unwiredPanic("push"))
	}
	err := store.Send(sctx, p.name, backends.PushMessage{
		Channel: p.name,
		Token:   msg.Token,
		Topic:   msg.Topic,
		Title:   msg.Title,
		Body:    msg.Body,
		Data:    msg.Data,
	})
	finish(err)
	return err
}

// IsUnregisteredPushToken reports whether err indicates that the target
// mobile push token is no longer registered with FCM/APNs and should be
// removed from the device/user profile store.
func IsUnregisteredPushToken(err error) bool {
	return errors.Is(err, backends.ErrUnregisteredPushToken)
}
