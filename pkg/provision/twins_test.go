package provision

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

func twinsGraph(t *testing.T) *analyzer.AppGraph {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "testdata", "twins")
	// GOWORK=off: testdata modules are deliberately not in go.work —
	// they are fixtures, not workspace members.
	g, err := analyzer.Analyze(analyzer.Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err != nil {
		t.Fatalf("analyze twins: %v", err)
	}
	return g
}

// A preset and the hand-written condition meant to replace it must
// compile to the SAME PromQL.
//
// This is the claim FA2 and FA3 exist to make good on: a preset is a
// starting point, not a ceiling — read what it compiles to with
// `tako alerts`, then write your own version with the threshold, window
// or label you actually wanted. If the raw vocabulary cannot restate
// what a preset says, that invitation is a lie, and the way it would
// fail is quiet: a fork that scopes one label differently is a query
// that matches nothing and an alert that reads as healthy.
//
// The pairs live in testdata/twins as <Name>Preset and <Name>ByHand.
func TestPresetsAndTheirHandWrittenTwinsAgree(t *testing.T) {
	cfg := Config{Project: "p", Region: "us-central1", App: "twins", Env: "x1"}
	described, err := DescribeAlerts(cfg, twinsGraph(t))
	if err != nil {
		t.Fatalf("describe: %v", err)
	}

	byName := map[string]AlertDescription{}
	for _, d := range described {
		byName[d.Name] = d
	}
	pairs := 0
	for name, preset := range byName {
		base, isPreset := strings.CutSuffix(name, "Preset")
		if !isPreset {
			continue
		}
		hand, ok := byName[base+"ByHand"]
		if !ok {
			// A preset with no twin is a coverage hole, and reporting it
			// is what stops this test from quietly covering less over
			// time.
			t.Errorf("%s has no %sByHand twin — either write one or say why the preset cannot be restated",
				name, base)
			continue
		}
		pairs++
		if preset.Query != hand.Query {
			t.Errorf("%s and %sByHand compile differently.\n"+
				"  A preset you cannot restate by hand is a ceiling, not a starting point.\n"+
				"  preset: %s\n  byhand: %s", name, base, preset.Query, hand.Query)
		}
	}
	if pairs < 5 {
		t.Fatalf("only %d preset/twin pairs found — the scan broke, which would make this vacuous", pairs)
	}
	t.Logf("%d presets restated by hand, character for character", pairs)
}

// The twins must also SAY something sensible. A fork that compiles the
// same query but pages with a Go variable name is a worse alert than the
// preset it replaced, and the sentence is the part a human reads at 3am.
// The raw readings had exactly that defect once already.
func TestHandWrittenTwinsGetRealSentences(t *testing.T) {
	described, err := DescribeAlerts(Config{App: "twins", Env: "x1"}, twinsGraph(t))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, d := range described {
		if !strings.HasSuffix(d.Name, "ByHand") {
			continue
		}
		checked++
		if strings.Contains(d.Sentence, "twins.") {
			t.Errorf("%s pages with a Go variable name: %q", d.Name, d.Sentence)
		}
		if !strings.HasPrefix(d.Sentence, "twins/x1: ") {
			t.Errorf("%s has no app-scoped subject: %q", d.Name, d.Sentence)
		}
		if strings.TrimSpace(strings.TrimPrefix(d.Sentence, "twins/x1:")) == "" {
			t.Errorf("%s has an empty sentence", d.Name)
		}
	}
	if checked == 0 {
		t.Fatal("no hand-written twins found — the scan broke")
	}
}
