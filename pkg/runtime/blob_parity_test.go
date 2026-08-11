package runtime

// CR4: the three handler wrappers should differ only in how they ENCODE
// a result, and BlobHandler had drifted on two things that are not
// about encoding — the trace id it logs under, and whether the
// cross-service context reaches the handler at all.

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tako "github.com/gsandoval/tako"
)

// A blob route is a route: a propagated trace id belongs to the chain
// that started it, not to whichever wrapper happened to answer.
func TestABlobHandlerLogsUnderTheTraceItWasGiven(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	h := BlobHandler(tako.API("GET /f", func(ctx tako.Ctx, _ struct{}) (tako.Blob, error) {
		ctx.Log().Info("serving")
		return tako.Blob{Reader: strings.NewReader("x"), Size: 1}, nil
	}), nil)

	r := httptest.NewRequest("GET", "/f", nil)
	r.Header.Set(requestHeader, "abc123")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !strings.Contains(buf.String(), "abc123") {
		t.Errorf("the caller's request id did not reach the blob handler's logger:\n%s\n"+
			"a blob route that mints a fresh id breaks the chain tako logs correlates on", buf.String())
	}
}

// The deadline and the session id ride the context, and an outgoing
// Call reads them from there. A blob handler that calls another service
// used to propagate neither.
func TestABlobHandlerCarriesTheCrossServiceContext(t *testing.T) {
	var hasDeadline bool
	h := BlobHandler(tako.API("GET /f", func(ctx tako.Ctx, _ struct{}) (tako.Blob, error) {
		_, hasDeadline = ctx.Deadline()
		return tako.Blob{Reader: strings.NewReader("x"), Size: 1}, nil
	}), nil)

	r := httptest.NewRequest("GET", "/f", nil)
	r.Header.Set(deadlineHeader, time.Now().Add(30*time.Second).UTC().Format(time.RFC3339Nano))
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !hasDeadline {
		t.Error("the propagated deadline did not bound the blob handler — work it starts " +
			"outlives the caller that already gave up")
	}
}
