package docserver

// reference/cli.md is the only place a reader can learn what the CLI
// does without running it, and nothing checked that it named every
// command. Two were missing when this test was written — `tako setup`
// and `tako datastore create` — and they are not minor ones: setup is
// what grants an app the roles it needs, and datastore create makes the
// database whose location can never be changed. Neither appeared
// anywhere in the guide or the reference, so the documented path to a
// first deploy skipped both prerequisites.
//
// The dispatch switch in cmd/tako/main.go is the source of truth: a
// command a reader can type is a command the docs owe them a line about.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var dispatchCase = regexp.MustCompile(`(?m)^\tcase "([a-z]+)":`)

func TestEveryCLICommandIsInTheCLIReference(t *testing.T) {
	main, err := os.ReadFile(filepath.Join(repoRoot, "cmd/tako/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	commands := dispatchCase.FindAllSubmatch(main, -1)
	if len(commands) < 10 {
		t.Fatalf("found %d commands in cmd/tako/main.go — the dispatch switch moved, "+
			"and this test is reading the wrong thing", len(commands))
	}

	page, err := os.ReadFile(filepath.Join(repoRoot, "docs/reference/cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, m := range commands {
		if !strings.Contains(string(page), "tako "+string(m[1])) {
			missing = append(missing, string(m[1]))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("reference/cli.md never names %d command(s) the CLI answers to: %s\n"+
			"A command with no line in the reference can only be found by reading `tako --help`\n"+
			"or the source.", len(missing), strings.Join(missing, ", "))
	}
}
