package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Generated code is user-readable surface, so its exact text is under
// test for each example.

func TestHelloMain(t *testing.T) {
	goldenService(t, "../../examples/hello", "hello", "testdata/hello_main.golden")
}

// Shortlink is the first service with backend boot and body+path+query
// decoding.
func TestShortlinkMain(t *testing.T) {
	goldenService(t, "../../examples/shortlink", "shortlink", "testdata/shortlink_main.golden")
}

// Gallery is the first service with multipart decode and blob streaming.
func TestGalleryMain(t *testing.T) {
	goldenService(t, "../../examples/gallery", "gallery", "testdata/gallery_main.golden")
}

// The worker service's generated main: register + push endpoint.
func TestShortlinkWorkerMain(t *testing.T) {
	goldenService(t, "../../examples/shortlink", "shortlink-countclick", "testdata/shortlink_worker_main.golden")
}

func goldenService(t *testing.T, appDir, service, golden string) {
	t.Helper()
	abs, err := filepath.Abs(appDir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := analyzer.Analyze(analyzer.Config{Dir: abs})
	if err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	written, err := Generate(g, outDir)
	if err != nil {
		t.Fatal(err)
	}
	// Every service gets a main (async ones serve the push endpoint), and
	// every ROUTE-SERVING service also gets a test harness (AB2). This
	// golden checks the named service's main; the harness has its own.
	mains := 0
	for _, rel := range written {
		if filepath.Base(rel) == "main.go" {
			mains++
		}
	}
	if mains != len(g.Services) {
		t.Fatalf("written = %v, want one main per service (%d)", written, len(g.Services))
	}
	want := filepath.Join(".tako", "gen", service, "main.go")
	found := false
	for _, rel := range written {
		if rel == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("written = %v, missing %s", written, want)
	}
	got, err := os.ReadFile(filepath.Join(outDir, want))
	if err != nil {
		t.Fatal(err)
	}

	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	wantSrc, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s (run with -update to create it): %v", golden, err)
	}
	if string(got) != string(wantSrc) {
		t.Fatalf("%s differs.\n--- want\n%s--- got\n%s", golden, wantSrc, got)
	}
}

// The harness is generated code a person reads when a test surprises
// them, so its text is golden like every main. journal is the richest
// case: auth with a declared role, a status page, a site, a policy, a
// cacheable route, and every binder shape.
func TestJournalHarness(t *testing.T) {
	goldenHarness(t, "../../examples/journal", "journal", "testdata/journal_harness.golden")
}

// shop has no site and no pages — an API-only harness, which is the
// shape that would break if the site block were assumed.
func TestShopHarness(t *testing.T) {
	goldenHarness(t, "../../examples/shop", "shop", "testdata/shop_harness.golden")
}

func goldenHarness(t *testing.T, appDir, service, golden string) {
	t.Helper()
	abs, err := filepath.Abs(appDir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := analyzer.Analyze(analyzer.Config{Dir: abs})
	if err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	if _, err := Generate(g, outDir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(outDir, ".tako", "gen", service, harnessDir, harnessFile))
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s (run with -update to create it): %v", golden, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s differs.\n--- want\n%s\n--- got\n%s", golden, want, got)
	}
}

// The mounts a harness makes and the mounts a main makes come from ONE
// emitter, and this is the assertion that keeps them from drifting apart
// again — the drift being the entire reason AB2 exists.
func TestTheHarnessMountsWhatTheMainMounts(t *testing.T) {
	abs, err := filepath.Abs("../../examples/journal")
	if err != nil {
		t.Fatal(err)
	}
	g, err := analyzer.Analyze(analyzer.Config{Dir: abs})
	if err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	if _, err := Generate(g, outDir); err != nil {
		t.Fatal(err)
	}
	main, err := os.ReadFile(filepath.Join(outDir, ".tako", "gen", "journal", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	harness, err := os.ReadFile(filepath.Join(outDir, ".tako", "gen", "journal", harnessDir, harnessFile))
	if err != nil {
		t.Fatal(err)
	}
	mainMounts := mountPatterns(string(main))
	harnessMounts := mountPatterns(string(harness))
	if len(mainMounts) == 0 {
		t.Fatal("the main mounts nothing — this test is not testing anything")
	}
	for pattern, opts := range mainMounts {
		got, ok := harnessMounts[pattern]
		if !ok {
			t.Errorf("the main mounts %q and the harness does not", pattern)
			continue
		}
		if got != opts {
			t.Errorf("%q: main has options %q, harness has %q", pattern, opts, got)
		}
	}
	for pattern := range harnessMounts {
		if _, ok := mainMounts[pattern]; !ok {
			t.Errorf("the harness mounts %q and the main does not", pattern)
		}
	}
}

// mountPatterns maps each mux.Handle pattern to the runtime OPTIONS on
// it — RequireAuth, RequirePolicy, CacheFor — which is the half nothing
// used to check.
func mountPatterns(src string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "mux.Handle(\"") {
			continue
		}
		rest := line[len("mux.Handle(\""):]
		end := strings.Index(rest, "\"")
		if end < 0 {
			continue
		}
		pattern := rest[:end]
		var opts []string
		for _, opt := range []string{"runtime.RequireAuth()", "runtime.RequirePolicy(", "runtime.CacheFor("} {
			if strings.Contains(line, opt) {
				opts = append(opts, opt)
			}
		}
		out[pattern] = strings.Join(opts, ",")
	}
	return out
}

func TestMultiPackageBackgroundHandlersRegisteredLocallyInPrimaryMain(t *testing.T) {
	g := &analyzer.AppGraph{
		Module: "example.com/app",
		Services: []analyzer.Service{
			{Name: "app", Package: "example.com/app"},
			{Name: "workers-digest", Package: "example.com/app/workers"},
		},
		Resources: []analyzer.Resource{
			{Kind: "queue", Name: "digests", Package: "example.com/app/workers", RateLimit: 5, MaxAttempts: 3, MinBackoffSeconds: 10, MaxBackoffSeconds: 60},
		},
		Handlers: []analyzer.Handler{
			{Kind: "api", Service: "app", Package: "example.com/app", Var: "app.Home", Route: "GET /api/home"},
			{Kind: "worker", Service: "workers-digest", Package: "example.com/app/workers", Var: "workers.SendDigest", Binding: "digests"},
			{Kind: "cron", Service: "workers", Package: "example.com/app/workers", Var: "workers.Nightly", Binding: "every 24h"},
		},
	}
	outDir := t.TempDir()
	if _, err := Generate(g, outDir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(outDir, ".tako", "gen", "app", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		`workers "example.com/app/workers"`,
		`if os.Getenv("TAKO_ENV") == ""`,
		`runtime.SetQueueRate("digests", 5)`,
		`runtime.SetQueueDelivery("digests", 3, 10*time.Second, 60*time.Second)`,
		`runtime.RegisterWorker("SendDigest", workers.SendDigest)`,
		`runtime.RegisterCron("Nightly", workers.Nightly)`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated primary main.go missing %q:\n%s", want, src)
		}
	}
}
