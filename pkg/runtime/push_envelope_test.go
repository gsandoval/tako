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
	got, ok := pubsubEnvelope([]byte(envelope))
	if !ok || string(got) != payload {
		t.Fatalf("envelope unwrap = %q, %v", got, ok)
	}

	// Raw local deliveries pass through untouched — including JSON
	// payloads that merely resemble envelopes without a subscription.
	for _, raw := range []string{payload, `{"message":{"data":"aGk="}}`, `not json`} {
		if _, ok := pubsubEnvelope([]byte(raw)); ok {
			t.Errorf("false positive envelope for %q", raw)
		}
	}
}
