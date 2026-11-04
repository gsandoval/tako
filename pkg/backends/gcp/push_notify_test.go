package gcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/backends"
)

func TestPushStoreSendsFCMv1AndDetectsUnregisteredToken(t *testing.T) {
	var gotURL string
	var gotBody fcmSendRequest
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		if gotBody.Message.Token == "stale-token" {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"status":"UNREGISTERED"}}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"name":"projects/my-proj/messages/1"}`)),
			Header:     make(http.Header),
		}, nil
	})
	c := &Client{
		HC:   &http.Client{Transport: rt},
		Opts: Options{Project: "my-proj", App: "shop", Env: "prod"},
	}
	store := NewPushStore(c)

	err := store.Send(context.Background(), "alerts", backends.PushMessage{
		Token: "good-token",
		Title: "Hello",
		Body:  "World",
		Data:  map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("Send good-token: %v", err)
	}
	if gotURL != "https://fcm.googleapis.com/v1/projects/my-proj/messages:send" {
		t.Errorf("URL = %q", gotURL)
	}
	if gotBody.Message.Notification == nil || gotBody.Message.Notification.Title != "Hello" {
		t.Errorf("notification = %+v", gotBody.Message.Notification)
	}
	if gotBody.Message.Data["tako_channel"] != "alerts" || gotBody.Message.Data["k"] != "v" {
		t.Errorf("data = %+v", gotBody.Message.Data)
	}

	err = store.Send(context.Background(), "alerts", backends.PushMessage{
		Token: "stale-token",
		Title: "Ping",
	})
	if err != backends.ErrUnregisteredPushToken {
		t.Fatalf("err = %v, want ErrUnregisteredPushToken", err)
	}
}
