package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

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
// serves both — a non-envelope body passes through untouched. GCS
// bucket notifications (C5) arrive through the same envelope and are
// translated to the ObjectInfo wire shape subscriber code decodes.
func pubsubEnvelope(body []byte) ([]byte, int, map[string]string, bool) {
	var env struct {
		Message struct {
			Data       []byte            `json:"data"` // encoding/json base64-decodes
			Attributes map[string]string `json:"attributes"`
		} `json:"message"`
		Subscription    string `json:"subscription"`
		DeliveryAttempt int    `json:"deliveryAttempt"`
	}
	if json.Unmarshal(body, &env) != nil || env.Subscription == "" || env.Message.Data == nil {
		return nil, 0, nil, false
	}
	if payload, ok := gcsNotification(env.Message.Attributes, env.Message.Data); ok {
		return payload, env.DeliveryAttempt, env.Message.Attributes, true
	}
	return env.Message.Data, env.DeliveryAttempt, env.Message.Attributes, true
}

// ReplayAttr addresses a replayed dead letter at one subscriber; see
// pkg/provision/gcp.ReplayAttr for why the message goes back through the
// topic rather than at the subscriber directly. Duplicated rather than
// imported: the runtime must not depend on the provisioner.
const ReplayAttr = "tako-replay-to"

// addressedElsewhere reports whether a replayed message names a
// DIFFERENT service than this one. A topic fans out to every
// subscriber; a dead letter belongs to exactly one, so everyone else
// acks and does nothing. Messages with no replay attribute — every
// normal delivery — are for whoever receives them.
func addressedElsewhere(attrs map[string]string) (string, bool) {
	to := attrs[ReplayAttr]
	if to == "" {
		return "", false
	}
	return to, to != os.Getenv("TAKO_SERVICE")
}

// gcsNotification translates a GCS OBJECT_FINALIZE notification
// (JSON_API_V1 object resource, sizes as strings) into the ObjectInfo
// wire shape local's bucket events publish — subscriber code sees one
// payload everywhere. Non-notification messages pass through.
func gcsNotification(attrs map[string]string, data []byte) ([]byte, bool) {
	if attrs["eventType"] != "OBJECT_FINALIZE" {
		return nil, false
	}
	var obj struct {
		Name        string    `json:"name"`
		Size        string    `json:"size"`
		ContentType string    `json:"contentType"`
		Updated     time.Time `json:"updated"`
	}
	if json.Unmarshal(data, &obj) != nil || obj.Name == "" {
		return nil, false
	}
	size, _ := strconv.ParseInt(obj.Size, 10, 64)
	payload, err := json.Marshal(backends.ObjectMeta{
		Key:         obj.Name,
		Size:        size,
		ContentType: obj.ContentType,
		Updated:     obj.Updated,
	})
	if err != nil {
		return nil, false
	}
	return payload, true
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
		var attrs map[string]string
		if data, n, a, ok := pubsubEnvelope(payload); ok {
			payload, attempt, attrs = data, n, a
		}
		if to, elsewhere := addressedElsewhere(attrs); elsewhere {
			// Ack it: the message reached us only because the topic fans
			// out, and nacking would retry a delivery that will never be
			// ours. Logged, not silent — a replay touching every
			// subscriber should be visible.
			slog.Default().Info("tako: skipping replay addressed to another service",
				"handler", name, "replay-to", to)
			w.WriteHeader(http.StatusNoContent)
			return
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
