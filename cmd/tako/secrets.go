package main

// `tako secrets` (SC4): the value nobody could put anywhere.
//
// tako has never created a secret, and still does not decide one — the
// value is the user's data. What it can do is stop making them hand-run
// the command preflight prints, and stop making them discover the names
// one boot failure at a time.
//
// Two properties this file exists to hold, both about what it does NOT
// do. No value is ever printed (there is no code path from a stored
// value to stdout). No value is ever read from a flag, because a flag
// lands in shell history, in ps, and in any CI log that echoes its
// commands.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
	"github.com/gsandoval/tako/pkg/backends/local"
	"github.com/gsandoval/tako/pkg/provision/gcp"
)

const secretsUsage = `tako secrets — the values your app declares with tako.Secret

  tako secrets                  what this app declares, and which have a value
  tako secrets set <name>       value from stdin, or prompted with echo off
  tako secrets rm  <name>       delete it

Local by default: no --project means .env beside go.mod. Add
--project <id> [--env <env>] for the deployed app's Secret Manager.

A value is never printed and never taken from a flag.
`

// declaredSecret is one tako.Secret from the graph, and where it was
// declared — the position is what turns "stripe-key is missing" into
// something you can go and look at.
type declaredSecret struct {
	Name string
	Pos  string
}

func declaredSecrets(graph *analyzer.AppGraph) []declaredSecret {
	var out []declaredSecret
	for _, r := range graph.Resources {
		if r.Kind == "secret" {
			out = append(out, declaredSecret{Name: r.Name, Pos: r.Pos})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func runSecrets(args []string) error {
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		args = args[1:]
	}
	switch name {
	case "", "set", "rm":
	default:
		fmt.Fprint(os.Stderr, secretsUsage)
		os.Exit(2)
	}
	var ref string
	if name != "" {
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			ref = args[0]
			args = args[1:]
		} else {
			return fmt.Errorf("tako secrets %s wants a name: tako secrets %s <name>", name, name)
		}
	}
	// The same rule as tako users, and now a rule rather than a special
	// case: a command that writes DATA takes its project typed. One that
	// converges infrastructure may infer it, because re-running fixes a
	// wrong guess and nothing here does.
	t, rest, err := parseSecretsTarget(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unknown argument %q (tako secrets accepts --project, --env)", rest[0])
	}

	graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	declared := declaredSecrets(graph)
	// rm is exempt (CR14). An app that declares no secrets is exactly
	// the app whose last tako.Secret was just deleted, and the orphan
	// left in Secret Manager is what this command exists to remove —
	// refusing the whole command there made the documented safe case
	// unreachable.
	if len(declared) == 0 && name != "rm" {
		return fmt.Errorf("this app declares no secrets — tako.Secret(\"name\") is what puts one here\n" +
			"      (tako secrets rm <name> still works: an orphan is why)")
	}
	c := &secretsCmd{out: os.Stdout, target: t, declared: declared, ask: askTerminal}
	switch name {
	case "":
		return c.list()
	case "set":
		return c.set(ref)
	case "rm":
		return c.remove(ref)
	}
	return nil
}

type secretsCmd struct {
	out      io.Writer
	target   accountsTarget
	declared []declaredSecret
	ask      func(prompt string, secret bool) (string, error)

	// gcp is built at most once. list() asks state() per declared
	// secret, and each of those used to construct a fresh client —
	// re-reading application-default credentials every time, for an
	// answer that does not change within a run.
	gcp *gcp.Client
}

func (c *secretsCmd) header() {
	where := "local · .env"
	if c.target.isCloud() {
		where = "cloud · " + c.target.project + " / " + c.target.env
	}
	fmt.Fprintf(c.out, "tako secrets · %s\n", where)
}

// physical is the name in Secret Manager. It is NOT always the declared
// name: env prefixes it, except in prod, which goes unprefixed — so
// showing it beats letting an operator guess which console row is
// theirs.
func (c *secretsCmd) physical(name string) string {
	return gcpb.SecretName(c.target.env, name)
}

func (c *secretsCmd) list() error {
	c.header()
	rows := [][]string{{"NAME", "VALUE", "DECLARED AT"}}
	if c.target.isCloud() {
		rows = [][]string{{"NAME", "VALUE", "IN SECRET MANAGER", "DECLARED AT"}}
	}
	missing := 0
	for _, d := range c.declared {
		set, detail, err := c.state(d.Name)
		if err != nil {
			return err
		}
		value := "MISSING"
		if set {
			value = "set"
		} else {
			missing++
		}
		if c.target.isCloud() {
			rows = append(rows, []string{d.Name, value, detail, d.Pos})
		} else {
			rows = append(rows, []string{d.Name, value, d.Pos})
		}
	}
	writeTable(c.out, rows)
	switch {
	case missing == 0:
		fmt.Fprintf(c.out, "  %d declared, all set\n", len(c.declared))
	case c.target.isCloud():
		fmt.Fprintf(c.out, "  %d declared, %d without a value — those services refuse to boot\n",
			len(c.declared), missing)
	default:
		fmt.Fprintf(c.out, "  %d declared, %d without a value — tako dev refuses to boot\n",
			len(c.declared), missing)
	}
	return nil
}

// state answers "is there a value", never "what is it".
func (c *secretsCmd) state(name string) (set bool, detail string, err error) {
	if !c.target.isCloud() {
		values, err := local.LoadEnvFile(envPath())
		if err != nil {
			return false, "", err
		}
		_, ok := values[name]
		return ok, "", nil
	}
	client, err := c.client()
	if err != nil {
		return false, "", err
	}
	st, err := client.SecretState(context.Background(), c.physical(name))
	if err != nil {
		return false, "", err
	}
	if !st.Exists {
		return false, c.physical(name) + " (absent)", nil
	}
	return st.Versions > 0, fmt.Sprintf("%s · %d version(s)", st.Physical, st.Versions), nil
}

func (c *secretsCmd) set(name string) error {
	if err := c.mustBeDeclared(name); err != nil {
		return err
	}
	c.header()
	if !c.target.isCloud() {
		if err := mustBeGitignored(envPath()); err != nil {
			return err
		}
	}
	value, err := c.ask("  value: ", true)
	if err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("no value given — nothing was written")
	}
	// .env is one name=value per line with no quoting, so a value with a
	// newline in it cannot be represented — writing it produces a file
	// LoadEnvFile reads back as something else. Secret Manager has no
	// such limit (the payload is bytes), so the refusal points there
	// rather than sounding like tako will not hold the value at all.
	if !c.target.isCloud() && strings.Contains(value, "\n") {
		return fmt.Errorf("that value is more than one line, and .env cannot hold one — it is\n"+
			"      name=value per line, with no quoting.\n\n"+
			"      Multi-line values (a PEM key, a service-account JSON) work against a\n"+
			"      project, where Secret Manager stores the bytes as they are:\n"+
			"        tako secrets set %s --project <id>\n\n"+
			"      For local runs, put a single-line stand-in in .env.", name)
	}
	if !c.target.isCloud() {
		replaced, err := local.SetEnvValue(envPath(), name, value)
		if err != nil {
			return err
		}
		verb := "wrote"
		if replaced {
			verb = "replaced"
		}
		fmt.Fprintf(c.out, "  ✓ %s %s in .env\n", verb, name)
		return nil
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	created, err := client.SetSecret(context.Background(), c.physical(name), value)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(c.out, "  ✓ created %s and added its first version\n", c.physical(name))
	} else {
		fmt.Fprintf(c.out, "  ✓ added a new version of %s — services read the latest\n", c.physical(name))
	}
	return nil
}

// remove is the bottom rung of the ladder in the cloud, and a
// convenience locally.
//
// Deleting a Secret Manager secret destroys every version, permanently:
// there is no undelete, and the versions are the audit trail. So the
// name is typed, and --yes does not apply — the ladder reserves that
// flag for things a re-run can put back.
func (c *secretsCmd) remove(name string) error {
	c.header()
	if !c.target.isCloud() {
		removed, err := local.RemoveEnvValue(envPath(), name)
		if err != nil {
			return err
		}
		if !removed {
			fmt.Fprintf(c.out, "  %s is not in .env — nothing to remove\n", name)
			return nil
		}
		fmt.Fprintf(c.out, "  ✓ removed %s from .env\n", name)
		return nil
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	physical := c.physical(name)
	st, err := client.SecretState(context.Background(), physical)
	if err != nil {
		return err
	}
	if !st.Exists {
		return fmt.Errorf("%s does not exist in %s — nothing to delete", physical, c.target.project)
	}
	fmt.Fprintf(c.out, "  %s   %d version(s), created %s\n",
		physical, st.Versions, st.Created.Local().Format("2006-01-02"))
	fmt.Fprintf(c.out, "  this destroys the secret and all %d version(s), permanently — Secret\n", st.Versions)
	fmt.Fprintln(c.out, "  Manager has no undelete, and the versions ARE the audit trail.")
	// Declared and orphaned are different acts, and the message says
	// which one this is. An orphan is the safe case this command exists
	// for; a declared secret stops the next deploy and the next cold
	// start, which is worth a sentence of its own.
	if pos := c.declaredAt(name); pos != "" {
		fmt.Fprintf(c.out, "  %s IS declared, at %s. Every service that resolves it refuses to\n", name, pos)
		fmt.Fprintln(c.out, "  BOOT without a value — this does not degrade, it stops.")
	} else {
		fmt.Fprintf(c.out, "  %s is not declared by this app — an orphan, which is the safe case\n", name)
		fmt.Fprintln(c.out, "  for this command.")
	}
	typed, err := c.ask(fmt.Sprintf("  type the secret's name to confirm: %s\n  > ", name), false)
	if err != nil {
		return err
	}
	if strings.TrimSpace(typed) != name {
		return fmt.Errorf("that is not the name — nothing was deleted")
	}
	if err := client.DeleteSecret(context.Background(), physical); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "  ✓ destroyed %s and %d version(s)\n", physical, st.Versions)
	return nil
}

func (c *secretsCmd) declaredAt(name string) string {
	for _, d := range c.declared {
		if d.Name == name {
			return d.Pos
		}
	}
	return ""
}

// mustBeDeclared refuses a name nothing reads. Writing a value under an
// undeclared name is a silent no-op: the write succeeds and no service
// ever resolves it. Same check, same reason, as tako users' roles.
//
// rm does NOT use this — deleting an orphan is the safe case, and the
// name being undeclared is exactly why you are deleting it.
func (c *secretsCmd) mustBeDeclared(name string) error {
	var names []string
	for _, d := range c.declared {
		if d.Name == name {
			return nil
		}
		names = append(names, d.Name)
	}
	msg := fmt.Sprintf("%q is not a secret this app declares\n      declared: %s (tako.Secret)",
		name, strings.Join(names, ", "))
	if near := nearestRole(name, names); near != "" {
		msg += fmt.Sprintf("\n      did you mean %s?", near)
	}
	return fmt.Errorf("%s", msg)
}

func (c *secretsCmd) client() (*gcp.Client, error) {
	if c.gcp != nil {
		return c.gcp, nil
	}
	client, err := gcp.NewClient(context.Background(), c.target.project, "")
	if err != nil {
		return nil, err
	}
	c.gcp = client
	return client, nil
}

// envPath is where the local values live: beside go.mod, which is what
// runtime.BootLocal and tako dev read.
func envPath() string { return ".env" }

// mustBeGitignored refuses to write a secret into a file git will
// happily commit.
//
// The convention exists today only as a comment inside
// examples/gallery/.env. A comment is not a guard, and this is the
// worst failure this command could cause — so it is checked, once, at
// the moment it matters.
func mustBeGitignored(path string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return nil // not a git checkout's problem to solve
	}
	if err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return nil
	}
	if err := exec.Command("git", "check-ignore", "-q", path).Run(); err == nil {
		return nil
	}
	abs, _ := filepath.Abs(path)
	return fmt.Errorf("%s is not gitignored, and this writes a secret into it\n"+
		"      add it first — one line, once:\n"+
		"        echo '%s' >> .gitignore\n\n"+
		"      (examples/gallery commits a .env on purpose: a dummy value so the\n"+
		"       demo runs out of the box. That is a demo's choice, not a default.)",
		filepath.Base(abs), filepath.Base(abs))
}
