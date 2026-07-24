package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// pubsubEnvelope unwraps a Pub/Sub push envelope into the message
// payload. Real Pub/Sub pushes wrap the payload as base64 in
// {"message":{"data":…},"subscription":"…"}; local delivery posts the
// raw payload. Shape-detected so the same endpoint serves both — a
// non-envelope body passes through untouched.
func pubsubEnvelope(body []byte) ([]byte, bool) {
	var env struct {
		Message struct {
			Data []byte `json:"data"` // encoding/json base64-decodes
		} `json:"message"`
		Subscription string `json:"subscription"`
	}
	if json.Unmarshal(body, &env) != nil || env.Subscription == "" || env.Message.Data == nil {
		return nil, false
	}
	return env.Message.Data, true
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
		if data, ok := pubsubEnvelope(payload); ok {
			payload = data
		}
		pctx := &requestCtx{Context: r.Context(), log: slog.Default().With("handler", name, "source", source)}
		switch err := fn(pctx, payload); {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case backends.IsPermanent(err):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}
