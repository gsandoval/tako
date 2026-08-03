package runtime

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The per-endpoint metrics slice shipped with a working recorder that
// nothing ever wired: SetRequestMetrics existed, five tests proved it
// worked, and no boot path called it. Every request tako deployed
// recorded into a nil sink.
//
// The five tests passed because each one calls SetRequestMetrics itself.
// A test that injects its own dependency can never notice a missing
// injection — so this one boots and then looks at what boot left behind.
func TestBootLocalWiresTheRequestSink(t *testing.T) {
	SetRequestMetrics(nil)
	t.Cleanup(func() { SetRequestMetrics(nil) })
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if requestMetrics == nil {
		t.Fatal("BootLocal left the per-endpoint sink nil — every requests and request_ms record is dropped")
	}
}

// BootGCP needs a project, a metadata server, and Secret Manager, so no
// test can boot it. Its wiring is checked the only way that survives
// without them: by reading the source. The defect was one missing call,
// and a missing call is visible in text.
func TestEveryBootPathWiresTheRequestSink(t *testing.T) {
	src, err := os.ReadFile("boot.go")
	if err != nil {
		t.Fatal(err)
	}
	bodies := splitFuncs(string(src))
	for _, name := range []string{"BootGCP", "BootLocal"} {
		body, ok := bodies[name]
		if !ok {
			t.Errorf("%s is gone from boot.go — if it moved, move this check with it", name)
			continue
		}
		if !strings.Contains(body, "SetRequestMetrics(") {
			t.Errorf("%s never calls SetRequestMetrics — services booted this way export no "+
				"per-endpoint metrics, and every Erroring/SlowerThan/CronMissed alert over them is dead", name)
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
