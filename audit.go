package tako

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/trace"
	"github.com/gsandoval/tako/pkg/backends"
)

// Audit declares a typed security or compliance audit stream.
//
// Events recorded via AuditHandle.Record are emitted as structured
// NOTICE-severity Cloud Logging entries aligned with Google Cloud Audit
// Logs (carrying logging.googleapis.com/labels, authenticationInfo, and
// requestMetadata) so they can be queried alongside native GCP Admin
// Activity and Data Access audit logs via `tako audit`.
func Audit[E any](name string) *AuditHandle[E] {
	return &AuditHandle[E]{name: name}
}

// AuditHandle is a declared security/compliance audit stream for events of
// type E.
type AuditHandle[E any] struct {
	name string
}

// Name returns the declared name of the audit stream.
func (a *AuditHandle[E]) Name() string { return a.name }

// AuditRecord is a captured security/compliance audit entry.
type AuditRecord struct {
	Stream      string          `json:"stream"`
	ActorID     string          `json:"actor_id,omitempty"`
	ActorEmail  string          `json:"actor_email,omitempty"`
	CallerIP    string          `json:"caller_ip,omitempty"`
	CallerAgent string          `json:"caller_agent,omitempty"`
	DeviceID    string          `json:"device_id,omitempty"`
	TraceID     string          `json:"trace_id,omitempty"`
	Event       json.RawMessage `json:"event"`
	RecordedAt  time.Time       `json:"recorded_at"`
}

// Record emits an immutable security/compliance audit entry for event,
// automatically attaching the caller's identity (User), network metadata
// (Client), and distributed trace correlation from ctx.
func (a *AuditHandle[E]) Record(ctx Ctx, event E) {
	raw, _ := json.Marshal(event)
	rec := backends.AuditRecord{
		Stream:     a.name,
		Event:      raw,
		RecordedAt: time.Now().UTC(),
	}
	if ctx != nil {
		if u, ok := ctx.MaybeUser(); ok {
			rec.ActorID = u.ID
			rec.ActorEmail = u.Email
		}
		cl := ctx.Client()
		rec.CallerIP = cl.IP
		rec.CallerAgent = cl.UserAgent
		rec.DeviceID = cl.DeviceID
		if tc, ok := trace.FromContext(ctx); ok {
			rec.TraceID = tc.TraceID
		}
	}

	if app := appFrom(ctx); app != nil && app.Audits != nil {
		app.Audits.Record(rec)
	} else if hooks.Audits != nil {
		hooks.Audits.Record(rec)
	}

	if ctx != nil && ctx.Log() != nil {
		labels := map[string]string{
			"tako.audit": a.name,
		}
		if rec.ActorID != "" {
			labels["tako.actor"] = rec.ActorID
		}
		authInfo := map[string]any{
			"principalSubject": rec.ActorID,
			"principalEmail":   rec.ActorEmail,
		}
		reqMeta := map[string]any{
			"callerIp":                rec.CallerIP,
			"callerSuppliedUserAgent": rec.CallerAgent,
			"deviceId":                rec.DeviceID,
		}
		op := map[string]any{
			"id":       rec.TraceID,
			"producer": "tako.audit/" + a.name,
		}
		// slog.LevelInfo + 2 maps to Cloud Logging's NOTICE severity —
		// the standard severity used by Google Cloud Audit Logs.
		ctx.Log().Log(ctx, slog.LevelInfo+2, "audit."+a.name,
			slog.Any("logging.googleapis.com/labels", labels),
			slog.Any("logging.googleapis.com/operation", op),
			slog.Any("authenticationInfo", authInfo),
			slog.Any("requestMetadata", reqMeta),
			slog.String("auditStream", a.name),
			slog.Any("auditEvent", event),
		)
	}
}
