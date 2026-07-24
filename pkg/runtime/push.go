package runtime

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

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
