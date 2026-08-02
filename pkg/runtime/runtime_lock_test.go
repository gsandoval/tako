package runtime_test

// P3: pkg/runtime's exported surface is a compatibility contract in
// practice, and had nothing guarding it.
//
// Generated services live in the USER's repository and import this
// package by name — 33 of its symbols across the six examples. Renaming
// one breaks every checked-in .tako/gen/*/main.go until the generator
// runs again. That is the same class of promise the root package makes,
// and the root package has had sdk_lock.golden guarding it since S0.1
// while this had nothing.
//
// This is deliberately WEAKER than the SDK lock: it records only the
// names and signatures of exported functions and types, so internals
// stay free to move. Regenerate deliberately with:
//
//	go test ./pkg/runtime -run TestRuntimeLock -update
//
// A diff here is not a refusal — pkg/… is tako's internals and may
// change between versions (see docs/reference/README.md). It is a
// reminder that the change reaches generated code users have committed,
// and belongs in the release notes.

import (
	"flag"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

var update = flag.Bool("update", false, "rewrite testdata/runtime_lock.golden")

func TestRuntimeLock(t *testing.T) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedTypes}
	pkgs, err := packages.Load(cfg, "github.com/gsandoval/tako/pkg/runtime")
	if err != nil {
		t.Fatalf("loading pkg/runtime: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 || len(pkgs) != 1 {
		t.Fatal("pkg/runtime did not load cleanly")
	}
	pkg := pkgs[0].Types
	qual := types.RelativeTo(pkg)

	var lines []string
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.TypeName:
			// The NAME only: generated code names these types, it does
			// not depend on their fields.
			lines = append(lines, "type "+o.Name())
		default:
			lines = append(lines, types.ObjectString(obj, qual))
		}
	}
	slices.Sort(lines)
	got := strings.Join(lines, "\n") + "\n"

	golden := filepath.Join("testdata", "runtime_lock.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s (run with -update to create it): %v", golden, err)
	}
	if got != string(want) {
		t.Fatalf("pkg/runtime's exported surface differs from %s.\n"+
			"Generated services in users' repositories import these names, so a change here\n"+
			"reaches code they have committed. If it is deliberate, rerun with -update and\n"+
			"note it in the release notes.\n\n%s",
			golden, lockDiff(string(want), got))
	}
}

// lockDiff reports added and removed lines, in that order.
func lockDiff(want, got string) string {
	w := strings.Split(strings.TrimSpace(want), "\n")
	g := strings.Split(strings.TrimSpace(got), "\n")
	inWant := map[string]bool{}
	for _, l := range w {
		inWant[l] = true
	}
	inGot := map[string]bool{}
	for _, l := range g {
		inGot[l] = true
	}
	var b strings.Builder
	for _, l := range g {
		if !inWant[l] {
			b.WriteString("+ " + l + "\n")
		}
	}
	for _, l := range w {
		if !inGot[l] {
			b.WriteString("- " + l + "\n")
		}
	}
	return b.String()
}
