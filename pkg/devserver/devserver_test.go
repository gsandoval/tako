package devserver

// P7: the dev loop had no tests at all — 447 lines, and the first thing
// a new user meets. These cover the pure decisions inside it: what the
// banner says, which file writes are ignored, and which service the
// loop runs. The process half (watch → build → run → restart) stays on
// the manual gate scripts; it needs a real toolchain and a real port.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// The banner is the first sentence tako ever says about your app, and
// it is derived — a miscount here is a user reading the wrong shape of
// their own system.
func TestCountLine(t *testing.T) {
	cases := []struct {
		name  string
		graph *analyzer.AppGraph
		want  string
	}{
		{
			name: "one endpoint, singular throughout",
			graph: &analyzer.AppGraph{
				Services: []analyzer.Service{{Name: "hello"}},
				Handlers: []analyzer.Handler{{Kind: "api"}},
			},
			want: "1 service, 1 endpoint",
		},
		{
			name: "zero endpoints still pluralizes correctly",
			graph: &analyzer.AppGraph{
				Services: []analyzer.Service{{Name: "worker"}},
				Handlers: []analyzer.Handler{{Kind: "worker"}},
			},
			want: "1 service, 0 endpoints, 1 worker",
		},
		{
			name: "each async kind counted under its own name",
			graph: &analyzer.AppGraph{
				Services: []analyzer.Service{{Name: "gallery"}, {Name: "gallery-makethumb"}},
				Handlers: []analyzer.Handler{
					{Kind: "api"}, {Kind: "page"},
					{Kind: "worker"},
					{Kind: "subscribe"}, {Kind: "subscribe"},
					{Kind: "cron"},
				},
			},
			want: "2 services, 2 endpoints, 1 worker, 2 subscribers, 1 cron",
		},
		{
			name: "a site is named, and only sites count as sites",
			graph: &analyzer.AppGraph{
				Services:  []analyzer.Service{{Name: "journal"}},
				Handlers:  []analyzer.Handler{{Kind: "page"}},
				Resources: []analyzer.Resource{{Kind: "site"}, {Kind: "entity"}},
			},
			want: "1 service, 1 endpoint, 1 site",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CountLine(c.graph); got != c.want {
				t.Errorf("CountLine = %q, want %q", got, c.want)
			}
		})
	}
}

// The loop writes into .tako/ every cycle. If those writes counted as
// source changes the server would reload forever, so this filter is
// load-bearing rather than cosmetic.
func TestHiddenPathIgnoresTheLoopsOwnWrites(t *testing.T) {
	dir := "/app"
	hidden := []string{
		"/app/.tako/gen/hello/main.go",
		"/app/.tako/data/entities.db",
		"/app/.git/index",
		"/app/.env",
		"/app/pkg/.hidden/x.go",
	}
	for _, p := range hidden {
		if !hiddenPath(dir, filepath.FromSlash(p)) {
			t.Errorf("hiddenPath(%s) = false — the loop would reload on its own write", p)
		}
	}
	visible := []string{
		"/app/app.go",
		"/app/web/index.html",
		"/app/internal/thumbs/thumbs.go",
	}
	for _, p := range visible {
		if hiddenPath(dir, filepath.FromSlash(p)) {
			t.Errorf("hiddenPath(%s) = true — a real edit would not reload", p)
		}
	}
	// A path outside the watched tree is not "hidden", it is irrelevant;
	// reporting it hidden would be the right answer for the wrong reason.
	if hiddenPath(dir, filepath.FromSlash("/elsewhere/.secret/x")) {
		t.Error("a path outside dir was judged by dir's rules")
	}
}

// tako dev runs ONE service — the app service. Jobs and singletons run
// in-process, and async-only services have no traffic to serve, so
// picking the wrong one means the dev server answers nothing.
func TestAppServiceIsTheOneThatServesTraffic(t *testing.T) {
	g := &analyzer.AppGraph{
		Services: []analyzer.Service{
			{Name: "gallery", Package: "gallery"},
			{Name: "gallery-makethumb", Package: "gallery"},
			{Name: "gallery-settle", Package: "gallery", Job: true},
			{Name: "gallery-invoicer", Package: "gallery", Singleton: true},
		},
		Handlers: []analyzer.Handler{
			{Kind: "api", Service: "gallery"},
			{Kind: "worker", Service: "gallery-makethumb"},
		},
	}
	svc, err := appService(g)
	if err != nil {
		t.Fatalf("appService: %v", err)
	}
	if svc.Name != "gallery" {
		t.Errorf("appService = %q, want gallery", svc.Name)
	}
}

// An app with nothing public is a real thing to write — a repo of only
// workers — and the dev server has to say so rather than pick one at
// random or serve nothing silently.
func TestAppServiceRefusesWhenNothingServes(t *testing.T) {
	g := &analyzer.AppGraph{
		Services: []analyzer.Service{{Name: "jobs-makethumb", Package: "jobs"}},
		Handlers: []analyzer.Handler{{Kind: "worker", Service: "jobs-makethumb"}},
	}
	if _, err := appService(g); err == nil {
		t.Error("an app with no public routes was accepted; dev would serve nothing")
	}
}

// A private-only package is not an app service either: WithPrivate means
// no public traffic, which is the whole point of the option.
func TestPrivateRoutesDoNotMakeAnAppService(t *testing.T) {
	g := &analyzer.AppGraph{
		Services: []analyzer.Service{{Name: "billing", Package: "billing"}},
		Handlers: []analyzer.Handler{{Kind: "api", Service: "billing", Private: true}},
	}
	if _, err := appService(g); err == nil {
		t.Error("a private-only service was treated as the app service")
	}
}

// Failure output is indented under its heading; a message that loses its
// indentation runs into the console's own lines.
func TestIndentKeepsEveryLine(t *testing.T) {
	got := indent("first\nsecond\n")
	if got != "  first\n  second" {
		t.Errorf("indent = %q", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Error("indent left a trailing newline, which doubles the blank line after it")
	}
}
