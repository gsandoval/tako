package gcp

import (
	"context"
	"errors"
	"strings"

	"github.com/gsandoval/tako/pkg/backends"
)

// PushStore implements backends.Push over the Firebase Cloud Messaging
// HTTP v1 API (POST https://fcm.googleapis.com/v1/projects/{project}/messages:send).
type PushStore struct {
	c *Client
}

// NewPushStore constructs a GCP FCM v1 PushStore.
func NewPushStore(c *Client) *PushStore {
	return &PushStore{c: c}
}

type fcmSendRequest struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token,omitempty"`
	Topic        string            `json:"topic,omitempty"`
	Notification *fcmNotification  `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
}

type fcmNotification struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

// Send dispatches msg via FCM HTTP v1 API and translates unregistered
// device token errors into backends.ErrUnregisteredPushToken.
func (s *PushStore) Send(ctx context.Context, channel string, msg backends.PushMessage) error {
	if (msg.Token == "" && msg.Topic == "") || (msg.Token != "" && msg.Topic != "") {
		return errors.New("tako: PushMessage must set exactly one of Token or Topic")
	}
	data := make(map[string]string, len(msg.Data)+1)
	for k, v := range msg.Data {
		data[k] = v
	}
	if channel != "" {
		data["tako_channel"] = channel
	}
	fcmMsg := fcmMessage{
		Token: msg.Token,
		Topic: msg.Topic,
		Data:  data,
	}
	if msg.Title != "" || msg.Body != "" {
		fcmMsg.Notification = &fcmNotification{
			Title: msg.Title,
			Body:  msg.Body,
		}
	}
	u := s.c.url(fcmHost, "/v1/projects/%s/messages:send", s.c.Opts.Project)
	err := s.c.do(ctx, "POST", u, fcmSendRequest{Message: fcmMsg}, nil)
	if err != nil {
		var se *statusError
		if errors.As(err, &se) {
			if se.Code == 404 || strings.Contains(se.Body, "UNREGISTERED") {
				return backends.ErrUnregisteredPushToken
			}
		}
		return err
	}
	return nil
}
