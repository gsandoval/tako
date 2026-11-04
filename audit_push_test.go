package tako

import (
	"encoding/json"
	"testing"
)

type securityAuditEvent struct {
	Action string `json:"action"`
	Target string `json:"target"`
}

var (
	securityAudit = Audit[securityAuditEvent]("security")
	mobilePush    = Push("mobile")
)

func TestAuditRecordCapturesIdentityTraceAndPayload(t *testing.T) {
	app := NewTestApp(t)
	admin := app.SignIn("alice")
	ctx := app.Ctx(admin)

	securityAudit.Record(ctx, securityAuditEvent{
		Action: "role.granted",
		Target: "bob@example.test",
	})

	recs := app.AuditRecords(securityAudit)
	if len(recs) != 1 {
		t.Fatalf("got %d audit records, want 1", len(recs))
	}
	r := recs[0]
	if r.Stream != "security" {
		t.Errorf("Stream = %q, want security", r.Stream)
	}
	if r.ActorID != admin.ID || r.ActorEmail != admin.Email {
		t.Errorf("Actor = (%q, %q), want (%q, %q)", r.ActorID, r.ActorEmail, admin.ID, admin.Email)
	}
	if r.TraceID == "" {
		t.Errorf("expected non-empty TraceID on audit record")
	}
	var ev securityAuditEvent
	if err := json.Unmarshal(r.Event, &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if ev.Action != "role.granted" || ev.Target != "bob@example.test" {
		t.Errorf("event = %+v", ev)
	}
}

func TestPushSendAndUnregisteredToken(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()

	err := mobilePush.Send(ctx, PushMessage{
		Token: "fcm-token-123",
		Title: "Order Shipped",
		Body:  "Your package is on the way",
		Data:  map[string]string{"order_id": "ord-99"},
	})
	if err != nil {
		t.Fatalf("mobilePush.Send: %v", err)
	}

	msgs := app.PushMessages(mobilePush)
	if len(msgs) != 1 {
		t.Fatalf("got %d push messages, want 1", len(msgs))
	}
	if msgs[0].Token != "fcm-token-123" || msgs[0].Title != "Order Shipped" || msgs[0].Data["order_id"] != "ord-99" {
		t.Fatalf("unexpected push message: %+v", msgs[0])
	}

	// Simulate an unregistered token and verify IsUnregisteredPushToken(err).
	app.app.Push.MarkUnregistered("fcm-token-stale")
	err = mobilePush.Send(ctx, PushMessage{
		Token: "fcm-token-stale",
		Title: "Ping",
	})
	if !IsUnregisteredPushToken(err) {
		t.Fatalf("err = %v, want IsUnregisteredPushToken(err) == true", err)
	}
}
