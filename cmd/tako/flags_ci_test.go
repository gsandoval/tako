package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/backends/local"
)

func TestFlagsCmdLifecycle(t *testing.T) {
	store := local.NewFlagStore(t.TempDir())
	var out bytes.Buffer
	cmd := &flagsCmd{
		out:    &out,
		target: accountsTarget{},
		declared: []declaredFlag{
			{Name: "checkout-v2", Type: "bool", Pos: "app.go:10"},
			{Name: "theme", Type: "string", Pos: "app.go:11"},
		},
		store: store,
	}

	// 1. Initial list shows default
	if err := cmd.list(); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "checkout-v2") || !strings.Contains(out.String(), "(default)") {
		t.Fatalf("expected default rule in list:\n%s", out.String())
	}

	// 2. Set fixed value
	out.Reset()
	if err := cmd.set("checkout-v2", "true"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !strings.Contains(out.String(), "✓ set checkout-v2 = true") {
		t.Fatalf("unexpected set output:\n%s", out.String())
	}

	// 3. Rollout percentage
	out.Reset()
	if err := cmd.rollout([]string{"checkout-v2", "true", "--pct", "25"}); err != nil {
		t.Fatalf("rollout: %v", err)
	}
	if !strings.Contains(out.String(), "25% of subjects") {
		t.Fatalf("unexpected rollout output:\n%s", out.String())
	}

	// 4. Split variants
	out.Reset()
	if err := cmd.split("theme", []string{"dark=50", "light=50"}); err != nil {
		t.Fatalf("split: %v", err)
	}
	if !strings.Contains(out.String(), "split theme across 2 variants") {
		t.Fatalf("unexpected split output:\n%s", out.String())
	}

	// 5. Verify list shows rollout and split
	out.Reset()
	if err := cmd.list(); err != nil {
		t.Fatalf("list after rules: %v", err)
	}
	listStr := out.String()
	if !strings.Contains(listStr, "rollout 25% -> true") {
		t.Errorf("expected rollout in list:\n%s", listStr)
	}
	if !strings.Contains(listStr, `split: "dark"=50%, "light"=50%`) {
		t.Errorf("expected split in list:\n%s", listStr)
	}

	// 6. Undeclared flag rejected
	if err := cmd.set("unknown-flag", "true"); err == nil {
		t.Fatal("expected error for undeclared flag")
	}

	// 7. Reset
	out.Reset()
	if err := cmd.reset("checkout-v2"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !strings.Contains(out.String(), "reset checkout-v2") {
		t.Fatalf("unexpected reset output:\n%s", out.String())
	}
}

func TestRunCIInit(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)

	if err := runCI([]string{"init", "--project", "my-gcp-proj", "--region", "europe-west1"}); err != nil {
		t.Fatalf("runCI: %v", err)
	}

	wfPath := filepath.Join(dir, ".github", "workflows", "tako.yml")
	b, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatalf("reading generated workflow: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		"GOOGLE_CLOUD_PROJECT: my-gcp-proj",
		"TAKO_REGION: europe-west1",
		"tako check",
		"tako test",
		"tako deploy --plan",
		"tako deploy --yes",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("workflow missing %q in:\n%s", want, s)
		}
	}
}
