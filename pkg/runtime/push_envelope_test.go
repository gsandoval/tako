package runtime

import (
	"encoding/base64"
	"fmt"
	"testing"
)

func TestPubsubEnvelope(t *testing.T) {
	payload := `{"id":"clk_1","slug":"gh"}`
	envelope := fmt.Sprintf(`{"message":{"data":%q,"messageId":"m1"},"subscription":"projects/p/subscriptions/s"}`,
		base64.StdEncoding.EncodeToString([]byte(payload)))
	got, attempt, ok := pubsubEnvelope([]byte(envelope))
	if !ok || string(got) != payload || attempt != 0 {
		t.Fatalf("envelope unwrap = %q, attempt %d, %v", got, attempt, ok)
	}

	// With a dead-letter policy the broker counts deliveries.
	envelope = fmt.Sprintf(`{"message":{"data":%q},"subscription":"projects/p/subscriptions/s","deliveryAttempt":3}`,
		base64.StdEncoding.EncodeToString([]byte(payload)))
	if _, attempt, ok := pubsubEnvelope([]byte(envelope)); !ok || attempt != 3 {
		t.Fatalf("deliveryAttempt = %d, %v, want 3", attempt, ok)
	}

	// Raw local deliveries pass through untouched — including JSON
	// payloads that merely resemble envelopes without a subscription.
	for _, raw := range []string{payload, `{"message":{"data":"aGk="}}`, `not json`} {
		if _, _, ok := pubsubEnvelope([]byte(raw)); ok {
			t.Errorf("false positive envelope for %q", raw)
		}
	}
}

// TestGCSNotificationTranslation pins the C5 bridge: a GCS
// OBJECT_FINALIZE notification (JSON_API_V1, string sizes) becomes the
// exact ObjectInfo wire shape local's bucket events publish, so
// subscriber code cannot tell the backends apart. Everything else
// passes through untranslated.
func TestGCSNotificationTranslation(t *testing.T) {
	object := `{"kind":"storage#object","name":"cat.png","bucket":"p-app-photos","size":"1234","contentType":"image/png","updated":"2026-07-25T12:00:00Z","generation":"1"}`
	envelope := fmt.Sprintf(
		`{"message":{"data":%q,"attributes":{"eventType":"OBJECT_FINALIZE","bucketId":"p-app-photos","objectId":"cat.png","payloadFormat":"JSON_API_V1"}},"subscription":"projects/p/subscriptions/s"}`,
		base64.StdEncoding.EncodeToString([]byte(object)))
	got, _, ok := pubsubEnvelope([]byte(envelope))
	if !ok {
		t.Fatal("notification envelope not detected")
	}
	want := `{"key":"cat.png","size":1234,"contentType":"image/png","updated":"2026-07-25T12:00:00Z"}`
	if string(got) != want {
		t.Fatalf("translated = %s, want %s", got, want)
	}

	// A tako-published message with other attributes is untouched.
	envelope = fmt.Sprintf(
		`{"message":{"data":%q,"attributes":{"tako-topic":"photo-ready"}},"subscription":"projects/p/subscriptions/s"}`,
		base64.StdEncoding.EncodeToString([]byte(`{"photoID":"x"}`)))
	if got, _, _ := pubsubEnvelope([]byte(envelope)); string(got) != `{"photoID":"x"}` {
		t.Fatalf("tako payload mangled: %s", got)
	}
}
