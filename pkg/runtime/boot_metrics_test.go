package runtime

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	tako "github.com/gsandoval/tako"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends/local"
)

// The per-endpoint metrics slice shipped with a working recorder that
// nothing ever wired: SetRequestMetrics existed, five tests proved it
// worked, and no boot path called it. Every request tako deployed
// recorded into a nil sink.
//
// The five tests passed because each one calls SetRequestMetrics itself.
// A test that injects its own dependency can never notice a missing
// injection — so this file boots and then looks at what boot left
// behind.
//
// The first fix added the missing call, once per boot path. Then
// tako.NewTestApp arrived as a THIRD boot, knew nothing about the call,
// and the harness served every request into nil again — the same defect,
// found the same way, a slice later. So there is no call to forget now:
// the sink IS the wired metrics backend. These tests boot each way that
// can be booted and then make a real request, which is the only shape a
// fourth boot path cannot pass by accident.
func TestALocalBootRecordsRequests(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assertRequestRecorded(t)
}

// NewTestApp is the boot an app's own tests run on, and app.Handler()
// serves through this very middleware — so a request under `tako test`
// has to reach a live sink for the same reason a deployed one does.
func TestATestAppBootRecordsRequests(t *testing.T) {
	tako.NewTestApp(t)
	assertRequestRecorded(t)
}

// assertRequestRecorded serves one request through the middleware and
// fails unless the wired backend saw it.
func assertRequestRecorded(t *testing.T) {
	t.Helper()
	log, ok := hooks.Metrics.(*local.MetricsLog)
	if !ok {
		t.Fatalf("boot left hooks.Metrics as %T — every requests and request_ms record is dropped", hooks.Metrics)
	}
	seen := map[string]bool{}
	log.OnMetric(func(_, name string, _ float64, _ int64) { seen[name] = true })
	t.Cleanup(func() { log.OnMetric(nil) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(w http.ResponseWriter, r *http.Request) {})
	logRequests(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	for _, name := range []string{RequestsMetric, LatencyMetric} {
		if !seen[name] {
			t.Errorf("%s never reached the wired sink — the per-endpoint alerts over it are dead", name)
		}
	}
}

// BootGCP needs a project, a metadata server, and Secret Manager, so no
// test can boot it. Its wiring is checked the only way that survives
// without them: by reading the source. A missing assignment is visible
// in text.
func TestTheCloudBootWiresTheMetricsBackend(t *testing.T) {
	src, err := os.ReadFile("boot.go")
	if err != nil {
		t.Fatal(err)
	}
	// bootGCP is the shared body: BootGCP reads the env and BootGCPWith
	// takes a target (SC5), and both land here. Checking the body rather
	// than either entry point is what keeps this true if a third way in
	// ever arrives — the lesson item 38 charged full price for.
	body, ok := splitFuncs(string(src))["bootGCP"]
	if !ok {
		t.Fatal("bootGCP is gone from boot.go — if it moved, move this check with it")
	}
	if !strings.Contains(body, "hooks.Metrics = ") {
		t.Error("the cloud boot never assigns hooks.Metrics — deployed services would export no " +
			"per-endpoint metrics, and every Erroring/SlowerThan/CronMissed alert over them is dead")
	}
	// And both doors must reach it, or one of them boots a service that
	// is quietly different from the other's.
	for _, entry := range []string{"BootGCP", "BootGCPWith"} {
		if !strings.Contains(splitFuncs(string(src))[entry], "bootGCP(opts)") &&
			!strings.Contains(splitFuncs(string(src))[entry], "BootGCPWith(opts)") {
			t.Errorf("%s does not reach the shared cloud boot", entry)
		}
	}
}

// splitFuncs maps top-level func names to their bodies. Package-level
// funcs close at a column-zero brace, which is enough here and needs no
// parser.
func splitFuncs(src string) map[string]string {
	decl := regexp.MustCompile(`(?m)^func (\w+)\(`)
	locs := decl.FindAllStringSubmatchIndex(src, -1)
	out := make(map[string]string, len(locs))
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out[src[loc[2]:loc[3]]] = src[loc[1]:end]
	}
	return out
}

// SC5. The target of a CLI run is an argument, not an ambient fact —
// flauta's tool had to set TAKO_PROJECT in its own process and then call
// BootGCP, which is a program mutating its environment to configure a
// library.
//
// Booting for real needs a project, a metadata server and Secret
// Manager, so what is testable is the refusal: the fields that must be
// there, and the fact that the message is not the generated main's.
func TestBootGCPWithNamesTheFieldItNeeds(t *testing.T) {
	for _, tc := range []struct {
		missing string
		opts    gcpb.Options
	}{
		{"Project", gcpb.Options{App: "flauta", Env: "staging"}},
		{"App", gcpb.Options{Project: "p", Env: "staging"}},
		{"Env", gcpb.Options{Project: "p", App: "flauta"}},
	} {
		err := BootGCPWith(tc.opts)
		if err == nil {
			t.Fatalf("Options with no %s must be refused", tc.missing)
		}
		if !strings.Contains(err.Error(), "Options."+tc.missing) {
			t.Errorf("refusal does not name the missing field:\n%s", err)
		}
		// The generated main's message blames the deploy, correctly, and
		// would be nonsense here: nobody injected anything.
		if strings.Contains(err.Error(), "this is a tako bug") {
			t.Errorf("a hand-written caller was told its own mistake is tako's:\n%s", err)
		}
	}
}

// Env is required rather than defaulted to prod, and that is the whole
// safety property: it is the Datastore namespace, so a tool that
// defaulted would read and write production because a field was empty.
func TestBootGCPWithDoesNotAssumeProd(t *testing.T) {
	err := BootGCPWith(gcpb.Options{Project: "p", App: "flauta"})
	if err == nil {
		t.Fatal("an empty Env must be refused, not silently taken as prod")
	}
	if !strings.Contains(err.Error(), "Datastore namespace") {
		t.Errorf("the refusal should say why env cannot be assumed:\n%s", err)
	}
}
