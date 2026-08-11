package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/backends/local"
)

// sentinel is a value no other part of these tests produces, so finding
// it anywhere in output is unambiguous.
const sentinel = "s3cr3t-VALUE-do-not-print-4c1f"

func secretsFixture(t *testing.T) (*secretsCmd, *strings.Builder) {
	t.Helper()
	t.Chdir(t.TempDir())
	if _, err := local.SetEnvValue(".env", "notifier-key", sentinel); err != nil {
		t.Fatal(err)
	}
	out := &strings.Builder{}
	return &secretsCmd{
		out: out,
		declared: []declaredSecret{
			{Name: "notifier-key", Pos: "app.go:41:2"},
			{Name: "stripe-key", Pos: "app.go:42:2"},
		},
		ask: func(string, bool) (string, error) { return sentinel, nil },
	}, out
}

// The one property this command is. There is no --show and no get, and
// the way that stops being true is somebody adding a column "just for
// debugging" — so the test is not "does list have a value column", it
// is "can the value reach stdout by ANY path".
func TestNoCommandEverPrintsAValue(t *testing.T) {
	for _, run := range []struct {
		name string
		fn   func(c *secretsCmd) error
	}{
		{"list", func(c *secretsCmd) error { return c.list() }},
		{"set", func(c *secretsCmd) error { return c.set("stripe-key") }},
		{"rm", func(c *secretsCmd) error { return c.remove("notifier-key") }},
	} {
		t.Run(run.name, func(t *testing.T) {
			c, out := secretsFixture(t)
			// .gitignore so set() is not refused before it prints.
			if err := os.WriteFile(".gitignore", []byte(".env\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := run.fn(c); err != nil {
				t.Logf("%s: %v", run.name, err)
			}
			if strings.Contains(out.String(), sentinel) {
				t.Errorf("tako secrets %s put a secret value in its output:\n%s", run.name, out)
			}
			// And the error path is output too — a refusal that echoed
			// the value would leak it just as well.
			if err := run.fn(c); err != nil && strings.Contains(err.Error(), sentinel) {
				t.Errorf("tako secrets %s put a secret value in an error: %v", run.name, err)
			}
		})
	}
}

func TestListSaysWhichAreMissingAndWhereTheyWereDeclared(t *testing.T) {
	c, out := secretsFixture(t)
	if err := c.list(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"tako secrets · local · .env",
		"notifier-key", "set", "app.go:41:2",
		"stripe-key", "MISSING", "app.go:42:2",
		"2 declared, 1 without a value — tako dev refuses to boot",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
}

// The worst failure this command could cause. The convention lived only
// in a comment inside examples/gallery/.env, and a comment is not a
// guard.
func TestWritingIntoAnUnignoredEnvIsRefused(t *testing.T) {
	c, _ := secretsFixture(t)
	if err := exec.Command("git", "init", "-q").Run(); err != nil {
		t.Skip("git unavailable")
	}
	err := c.set("stripe-key")
	if err == nil {
		t.Fatal("writing a secret into a committable .env must be refused")
	}
	for _, want := range []string{
		".env is not gitignored, and this writes a secret into it",
		"echo '.env' >> .gitignore",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q:\n%s", want, err)
		}
	}
	// And with it ignored, the same call goes through.
	if err := os.WriteFile(".gitignore", []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.set("stripe-key"); err != nil {
		t.Fatalf("an ignored .env should be writable: %v", err)
	}
	values, _ := local.LoadEnvFile(".env")
	if values["stripe-key"] != sentinel {
		t.Errorf("the value did not land: %q", values["stripe-key"])
	}
}

func TestAnUndeclaredNameIsRefusedOnSet(t *testing.T) {
	c, _ := secretsFixture(t)
	err := c.set("stripe-ke")
	if err == nil {
		t.Fatal("writing under a name nothing reads is a silent no-op and must be refused")
	}
	for _, want := range []string{
		`"stripe-ke" is not a secret this app declares`,
		"declared: notifier-key, stripe-key (tako.Secret)",
		"did you mean stripe-key?",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q:\n%s", want, err)
		}
	}
}

// The .env belongs to the developer: a command that reformatted it, or
// dropped the comment explaining where a key came from, is one people
// stop using.
func TestWritingKeepsTheRestOfTheFileExactly(t *testing.T) {
	t.Chdir(t.TempDir())
	original := "# where this came from\nnotifier-key=old\n\n# grouped separately\nother=untouched\n"
	if err := os.WriteFile(".env", []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := local.SetEnvValue(".env", "notifier-key", "new"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(".env")
	want := "# where this came from\nnotifier-key=new\n\n# grouped separately\nother=untouched\n"
	if string(got) != want {
		t.Errorf("the file was reshaped:\n%q\nwant\n%q", got, want)
	}

	if _, err := local.RemoveEnvValue(".env", "notifier-key"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(".env")
	if strings.Contains(string(got), "notifier-key") {
		t.Errorf("rm left the key behind:\n%s", got)
	}
	if !strings.Contains(string(got), "# grouped separately") || !strings.Contains(string(got), "other=untouched") {
		t.Errorf("rm took something with it:\n%s", got)
	}
}

// A new .env is a secret file from its first byte.
func TestANewEnvIsNotWorldReadable(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := local.SetEnvValue(".env", "k", "v"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(".env")
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf(".env is mode %04o — a file holding secrets should not be readable by others", mode)
	}
}

// The command reads .env from the module directory, which is what
// runtime.BootLocal and tako dev read. A different path would make
// `tako secrets set` a writer nobody reads.
func TestTheLocalPathIsTheOneBootReads(t *testing.T) {
	if envPath() != ".env" {
		t.Errorf("envPath = %q, want .env — the file tako dev resolves secrets from", envPath())
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "pkg", "runtime", "boot.go"))
	if err != nil {
		t.Skip("runtime not readable")
	}
	if !strings.Contains(string(src), ".env") {
		t.Error("runtime.BootLocal no longer mentions .env — if the local secret source moved, " +
			"tako secrets has to move with it")
	}
}

// CR9: `cat key.pem | tako secrets set signing-key` stored the first
// line and reported success.
//
// askTerminal read with ReadString('\n'), so a piped value stopped at
// the first newline — silently, on the one command whose entire job is
// to place a value correctly. The reference documents stdin as the
// input, so the piped path is the supported one, and it was the one
// that lost data.
func TestAPipedValueIsReadWhole(t *testing.T) {
	got, err := readAll(strings.NewReader("-----BEGIN KEY-----\nline two\nline three\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "\n") != 2 {
		t.Errorf("a multi-line value was truncated to %q", got)
	}
	// One trailing newline is the operator's Enter, not part of the value.
	if strings.HasSuffix(got, "\n") {
		t.Errorf("the trailing newline survived: %q", got)
	}
}

// .env is one name=value per line and cannot hold a newline, so a
// multi-line value has to be REFUSED rather than written as something
// LoadEnvFile will read back wrong. Secret Manager has no such limit
// and takes it.
func TestAMultiLineValueIsRefusedLocallyRatherThanMangled(t *testing.T) {
	c, _ := secretsFixture(t)
	if err := os.WriteFile(".gitignore", []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.ask = func(string, bool) (string, error) { return "line one\nline two", nil }
	err := c.set("stripe-key")
	if err == nil {
		t.Fatal("a multi-line value was written into .env, which cannot represent it")
	}
	for _, want := range []string{"more than one line", "--project"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
	// And it must not have half-written it.
	values, readErr := local.LoadEnvFile(".env")
	if readErr != nil {
		t.Fatalf(".env is no longer parseable: %v", readErr)
	}
	if _, ok := values["stripe-key"]; ok {
		t.Error("the refused value was written anyway")
	}
}

// CR12: the refusal is shared with tako users, and used to name that
// command whichever one you ran.
func TestTheSecretsRefusalNamesSecrets(t *testing.T) {
	_, _, err := parseSecretsTarget([]string{"--env", "staging"})
	if err == nil {
		t.Fatal("--env without --project must be refused")
	}
	if strings.Contains(err.Error(), "tako users") || strings.Contains(err.Error(), "people") {
		t.Errorf("tako secrets was refused with tako users' message:\n%s", err)
	}
	for _, want := range []string{"tako secrets", "secrets", ".env"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q:\n%s", want, err)
		}
	}
}
