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
