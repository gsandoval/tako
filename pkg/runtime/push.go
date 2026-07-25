package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// deadLetter, when set (BootGCP), publishes one exhausted payload onto
// this service's own dead-letter topic — how tako.Permanent keeps its
// "one attempt, then dead-letter" semantics on a broker that can only
// retry nacks.
var deadLetter func(ctx context.Context, payload []byte, reason string) error

// pubsubEnvelope unwraps a Pub/Sub push envelope into the message
// payload and its delivery attempt (0 when the broker doesn't say —
// local posts, or subscriptions without a dead-letter policy). Real
// Pub/Sub pushes wrap the payload as base64 in
// {"message":{"data":…},"subscription":"…","deliveryAttempt":n}; local
// delivery posts the raw payload. Shape-detected so the same endpoint
// serves both — a non-envelope body passes through untouched.
func pubsubEnvelope(body []byte) ([]byte, int, bool) {
	var env struct {
		Message struct {
			Data []byte `json:"data"` // encoding/json base64-decodes
		} `json:"message"`
		Subscription    string `json:"subscription"`
		DeliveryAttempt int    `json:"deliveryAttempt"`
	}
	if json.Unmarshal(body, &env) != nil || env.Subscription == "" || env.Message.Data == nil {
		return nil, 0, false
	}
	return env.Message.Data, env.DeliveryAttempt, true
}

// PushHandler serves a per-service delivery endpoint: POST /_tako/push
// with the message payload as the body invokes the service's one async
// handler. This is how a worker/subscriber service receives work in the
// cloud (Pub/Sub push, Phase F adds the envelope + OIDC verification);
// locally it makes the generated async-service main a real, testable
// HTTP service. Status contract, matching Pub/Sub's ack rules:
//
//	nil            → 204 (ack)
//	error          → 500 (nack: the broker redelivers)
//	tako.Permanent → 400 (nack that must not retry: the broker's
//	                  dead-letter policy owns it — Phase F wires that)
func PushHandler(name string, h tako.Handler) http.Handler {
	kind, source, invoke := hooks.HandlerInfo(h)
	if kind != "worker" && kind != "subscribe" {
		panic(fmt.Sprintf("tako: PushHandler(%s): handler is %q, not a worker or subscriber", name, kind))
	}
	fn, ok := invoke.(func(tako.Ctx, []byte) error)
	if !ok {
		panic("tako: PushHandler before Boot")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read payload: "+err.Error(), http.StatusBadRequest)
			return
		}
		attempt := 0
		if data, n, ok := pubsubEnvelope(payload); ok {
			payload, attempt = data, n
		}
		log := slog.Default().With("handler", name, "source", source)
		if attempt > 0 {
			// maxAttempts mirrors the provisioner's deadLetterPolicy and
			// local's dispatcher; the broker owns the actual count.
			log = log.With("attempt", fmt.Sprintf("%d/5", attempt))
		}
		pctx := &requestCtx{Context: r.Context(), log: log}
		switch err := fn(pctx, payload); {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case backends.IsPermanent(err):
			// In the cloud, route the message to this service's own dead
			// topic and ACK: a 400 nack would be retried like any other
			// (Pub/Sub has no "permanent" nack). Locally the dispatcher
			// understands 400 as permanent, so it keeps the status.
			if deadLetter != nil {
				if dlErr := deadLetter(r.Context(), payload, err.Error()); dlErr != nil {
					log.Error("permanent failure could not dead-letter — nacking for redelivery", "err", err, "deadletter_err", dlErr)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				log.Warn("dead-lettered permanently failed delivery", "err", err)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			// The broker owns the retry; the narration is ours — the
			// cloud face of local's "attempt n/5" retry lines.
			log.Warn("delivery failed — nacked for retry", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}
