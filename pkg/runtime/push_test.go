package runtime

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	tako "github.com/gsandoval/tako"
)

type pushMsg struct {
	Mode string `json:"mode"`
}

var (
	pushQ      = tako.Queue[pushMsg]("push-contract-q")
	PushWorker = tako.Worker(pushQ, handlePush)
	pushSeen   []string
)

func handlePush(ctx tako.Ctx, m pushMsg) error {
	pushSeen = append(pushSeen, m.Mode)
	switch m.Mode {
	case "fail":
		return errors.New("transient")
	case "reject":
		return tako.Permanent(errors.New("bad message"))
	}
	return nil
}

// TestPushHandlerContract pins the push status mapping the cloud broker
// relies on: 204 ack, 500 retry, 400 permanent (dead-letter policy's
// job), including undecodable payloads (permanent — a payload that
// cannot decode never gets better).
func TestPushHandlerContract(t *testing.T) {
	h := PushHandler("PushWorker", PushWorker)
	post := func(body string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/_tako/api/push", strings.NewReader(body)))
		return rec.Code
	}

	pushSeen = nil
	if code := post(`{"mode":"ok"}`); code != 204 {
		t.Fatalf("ack status = %d, want 204", code)
	}
	if code := post(`{"mode":"fail"}`); code != 500 {
		t.Fatalf("retry status = %d, want 500", code)
	}
	if code := post(`{"mode":"reject"}`); code != 400 {
		t.Fatalf("permanent status = %d, want 400", code)
	}
	if code := post(`not json`); code != 400 {
		t.Fatalf("undecodable payload status = %d, want 400 (permanent)", code)
	}
	if len(pushSeen) != 3 { // the undecodable payload never reached the handler
		t.Fatalf("handler saw %v", pushSeen)
	}
}

// TestPushHandlerPermanentDeadLetters pins the cloud short-circuit: with
// the dead-letter hook wired (BootGCP), a Permanent failure publishes
// the payload to the service's own dead topic and ACKS — one attempt,
// then dead-letter, exactly local's semantics on a broker that can only
// retry nacks. A hook failure nacks so nothing is lost.
func TestPushHandlerPermanentDeadLetters(t *testing.T) {
	h := PushHandler("PushWorker", PushWorker)
	post := func(body string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/_tako/api/push", strings.NewReader(body)))
		return rec.Code
	}

	var gotPayload, gotReason string
	deadLetter = func(_ context.Context, payload []byte, reason string) error {
		gotPayload, gotReason = string(payload), reason
		return nil
	}
	defer func() { deadLetter = nil }()

	if code := post(`{"mode":"reject"}`); code != 204 {
		t.Fatalf("permanent-with-hook status = %d, want 204 (acked after dead-letter publish)", code)
	}
	if gotPayload != `{"mode":"reject"}` || !strings.Contains(gotReason, "bad message") {
		t.Fatalf("dead-lettered payload %q reason %q", gotPayload, gotReason)
	}
	// Transient errors never touch the hook: the broker owns retries.
	gotPayload = ""
	if code := post(`{"mode":"fail"}`); code != 500 || gotPayload != "" {
		t.Fatalf("transient with hook: status %d, dead-lettered %q", code, gotPayload)
	}

	deadLetter = func(context.Context, []byte, string) error { return errors.New("pubsub down") }
	if code := post(`{"mode":"reject"}`); code != 500 {
		t.Fatalf("hook-failure status = %d, want 500 (nack, redeliver)", code)
	}
}
