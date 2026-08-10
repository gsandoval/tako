package main

// `tako users` (SC1): the operator's door onto the accounts an app
// already has.
//
// Everything here goes through auth.Users() — the SDK surface AB10
// shipped — and never past it to a store. That is the whole design, and
// it is worth saying why, because the shortcut is tempting: "write the
// account, then mark its sessions stale, under a lease keyed on the
// account" is orchestration that lives in the root package's users.go.
// A second copy of it here would be a duplicate every future change has
// to be copied into, and the copy that gets missed is a role that never
// lands or a disabled user still signed in for a week. So this file
// imports no backend, wires nothing, and holds no account logic: it
// parses, it calls the SDK, and it prints. The wiring is usersboot.go's,
// and the split is enforced by a test.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gsandoval/tako"
	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/runtime"
)

const usersUsage = `tako users — the accounts your app's sign-in door admits

  tako users list                      every account, with its roles
  tako users show   <email|id>
  tako users create <email> [--name N] [--password] [--role R]...
  tako users grant  <email|id> <role>
  tako users revoke <email|id> <role>
  tako users disable <email|id>        sign-in refused, record kept
  tako users enable  <email|id>
  tako users delete  <email|id>        permanent

Local by default: no --project means .tako/data, the same accounts
` + "`tako dev`" + ` signs in. Add --project <id> [--env <env>] to work on a
deployed app's Identity Platform instead.
`

// accountsTarget is where the accounts live. There are two, and which
// one is NEVER inferred — see parseAccountsTarget.
type accountsTarget struct {
	project string
	env     string
}

func (t accountsTarget) isCloud() bool { return t.project != "" }

// describe is the first line of every run. The target is printed
// before anything happens, not mentioned in passing afterwards,
// because "which accounts am I about to change" is the question this
// command has to answer before it answers any other.
func (t accountsTarget) describe() string {
	if t.isCloud() {
		return "cloud · " + t.project + " / " + t.env
	}
	return "local · .tako/data"
}

// parseAccountsTarget resolves --project and --env, and DELIBERATELY
// does not fall back to GOOGLE_CLOUD_PROJECT or gcloud config the way
// parseTarget does for every other cloud command (D1, approved
// 2026-08-10).
//
// The other cloud commands read, or converge a project toward what the
// code already says; re-running them is how you fix a wrong guess. This
// one writes people. A project left in a shell from an unrelated task
// turns `tako users create` into a real account in a real project, and
// no later deploy takes it back out. So the cloud is somewhere you go
// on purpose, by typing it.
func parseAccountsTarget(raw []string) (accountsTarget, []string, error) {
	t := accountsTarget{}
	var envGiven bool
	// Both --flag value and --flag=value, like every other command.
	var args []string
	for _, a := range raw {
		if name, value, ok := strings.Cut(a, "="); ok && (name == "--project" || name == "--env") {
			args = append(args, name, value)
			continue
		}
		args = append(args, a)
	}
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "--env":
			flag := args[i]
			if i+1 >= len(args) {
				return t, nil, fmt.Errorf("%s needs a value", flag)
			}
			i++
			if flag == "--project" {
				t.project = args[i]
			} else {
				t.env, envGiven = args[i], true
			}
		default:
			rest = append(rest, args[i])
		}
	}
	if envGiven && t.project == "" {
		return t, nil, errors.New("--env names a cloud environment but no project was given\n" +
			"      add --project <id>, or drop --env to work on .tako/data\n\n" +
			"      tako users never takes the project from GOOGLE_CLOUD_PROJECT or from\n" +
			"      gcloud config the way the other cloud commands do: this one writes\n" +
			"      people, and a project inherited from a shell is how a dev fixture\n" +
			"      lands in production")
	}
	if t.isCloud() {
		if t.env == "" {
			t.env = defaultAccountsEnv
		}
		if err := checkEnv(t.env); err != nil {
			return t, nil, err
		}
	}
	return t, rest, nil
}

// accountsApp is what the graph knows that this command needs: whether
// there is a sign-in door at all, which roles exist, which providers
// were declared, and who is listening for a new account.
//
// It is a value rather than a *AppGraph so the command can be driven in
// tests without running go/packages over a fixture module.
type accountsApp struct {
	auth       bool
	authPos    string
	providers  []string
	roles      []string
	registered []string // vars subscribed to auth.Registered
}

func accountsAppFrom(graph *analyzer.AppGraph) accountsApp {
	app := accountsApp{roles: graph.Roles}
	for _, r := range graph.Resources {
		if r.Kind == "auth" {
			app.auth = true
			app.authPos = r.Pos
			app.providers = r.Providers
		}
	}
	for _, h := range graph.Handlers {
		if h.Kind == "subscribe" && h.Binding == authRegisteredSource {
			app.registered = append(app.registered, shortVar(h.Var))
		}
	}
	return app
}

// authRegisteredSource is the topic auth.Registered publishes to. It is
// spelled here rather than imported because internal/hooks owns it for
// the runtime's sake; a mismatch is caught by the test that builds a
// graph from the real constant.
const authRegisteredSource = "tako-auth-registered"

// shortVar drops the package qualifier: a transcript names welcomeMail,
// not shop/mail.WelcomeMail.
func shortVar(v string) string {
	if i := strings.LastIndex(v, "."); i >= 0 {
		return v[i+1:]
	}
	return v
}

// usersCmd is one run, after the target is resolved and the backends
// are wired.
type usersCmd struct {
	out    io.Writer
	app    accountsApp
	target accountsTarget
	// ask reads one line from the operator: a password (secret) or a
	// typed confirmation. A seam, so the golden tests drive the
	// confirmation ladder without a terminal.
	ask func(prompt string, secret bool) (string, error)
}

// runUsers is the command entry: parse, learn what the app declares,
// wire the chosen target, run.
func runUsers(args []string) error {
	if len(args) == 0 || !knownUsersCommand(args[0]) {
		fmt.Fprint(os.Stderr, usersUsage)
		os.Exit(2)
	}
	name, rest := args[0], args[1:]
	t, rest, err := parseAccountsTarget(rest)
	if err != nil {
		return err
	}
	graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	app := accountsAppFrom(graph)
	// The graph answers first, before a single credential is touched:
	// no sign-in door, an undeclared role and a malformed address are
	// all mistakes that should cost no round trip and produce no 403.
	if !app.auth {
		return errors.New("this app declares no tako.Auth — accounts exist because an app has a\n" +
			"      sign-in door. Add tako.Auth(...) and the accounts follow")
	}
	c := &usersCmd{out: os.Stdout, app: app, target: t, ask: askTerminal}
	if err := c.check(name, rest); err != nil {
		return err
	}
	closeAccounts, err := openAccounts(t, graph)
	if err != nil {
		return err
	}
	defer closeAccounts()
	return c.run(name, rest)
}

// check is the graph-only half of validation, run before wiring.
func (c *usersCmd) check(name string, args []string) error {
	switch name {
	case "grant", "revoke":
		if len(args) < 2 {
			return fmt.Errorf("tako users %s wants an account and a role: tako users %s <email|id> <role>", name, name)
		}
		return c.checkRole(args[1])
	case "create":
		if len(args) < 1 {
			return errors.New("tako users create wants an email: tako users create <email> [--name N] [--password]")
		}
		for _, r := range roleFlags(args[1:]) {
			if err := c.checkRole(r); err != nil {
				return err
			}
		}
	}
	return nil
}

// knownUsersCommand gates the usage exit, so an unknown verb prints the
// commands rather than an error about an account it never looked for.
func knownUsersCommand(name string) bool {
	switch name {
	case "list", "show", "create", "grant", "revoke", "disable", "enable", "delete":
		return true
	}
	return false
}

// checkRole refuses a role the app does not declare.
//
// This is not pedantry about spelling: a role is a claim, and granting
// one no policy reads writes a claim nothing reads. The store accepts
// it, the operator sees success, and the person still cannot get in.
// The analyzer already collects the declared set (AppGraph.Roles, for
// the dev sign-in page), so the check costs nothing.
//
// There is deliberately no --force. An escape hatch on a check whose
// whole value is catching a typo defeats the check.
func (c *usersCmd) checkRole(name string) error {
	for _, r := range c.app.roles {
		if r == name {
			return nil
		}
	}
	if len(c.app.roles) == 0 {
		return fmt.Errorf("%q is not a role this app declares — it declares none\n"+
			"      a role is declared with tako.Role(%q), and read by a route with\n"+
			"      tako.Route{Allow: tako.RequireRole(...)}", name, name)
	}
	msg := fmt.Sprintf("%q is not a role this app declares\n      declared: %s (tako.Role)",
		name, strings.Join(c.app.roles, ", "))
	if near := nearestRole(name, c.app.roles); near != "" {
		msg += fmt.Sprintf("\n      did you mean %s?", near)
	}
	return errors.New(msg)
}

// nearestRole is the did-you-mean: the closest declared role within two
// edits, and "" when nothing is close enough to suggest. A hint that
// names something unrelated is worse than no hint.
func nearestRole(typed string, declared []string) string {
	best, bestDist := "", 3
	for _, r := range declared {
		if d := editDistance(typed, r); d < bestDist {
			best, bestDist = r, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func (c *usersCmd) run(name string, args []string) error {
	fmt.Fprintf(c.out, "tako users · %s\n", c.target.describe())
	switch name {
	case "list":
		return c.list()
	case "show":
		return c.show(args)
	case "create":
		return c.create(args)
	case "grant":
		return c.setRole(args[0], args[1], true)
	case "revoke":
		return c.setRole(args[0], args[1], false)
	case "disable":
		return c.setEnabled(args, false)
	case "enable":
		return c.setEnabled(args, true)
	case "delete":
		return c.remove(args)
	}
	return fmt.Errorf("unknown users command %q", name)
}

// users is the SDK handle and a ctx to call it with. UsersHandle holds
// no state — it reads the wired backends — so constructing one here is
// the same handle auth.Users() returns inside a service.
func (c *usersCmd) users() (*tako.UsersHandle, tako.Ctx) {
	return &tako.UsersHandle{}, runtime.NewCtx(context.Background())
}

func (c *usersCmd) list() error {
	users, ctx := c.users()
	var all []tako.User
	for cursor := ""; ; {
		page, err := users.List(ctx).Limit(200).Page(cursor)
		if err != nil {
			return c.cloudErr(err)
		}
		all = append(all, page.Users...)
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(all) == 0 {
		fmt.Fprintln(c.out, "  no accounts yet — tako users create <email> makes the first one")
		return nil
	}
	rows := [][]string{{"ID", "EMAIL", "NAME", "ROLES"}}
	for _, u := range all {
		rows = append(rows, []string{u.ID, u.Email, u.Name, strings.Join(u.Roles, ", ")})
	}
	writeTable(c.out, rows)
	fmt.Fprintf(c.out, "  %d account%s\n", len(all), plural(len(all)))
	return nil
}

func (c *usersCmd) show(args []string) error {
	if len(args) < 1 {
		return errors.New("tako users show wants an account: tako users show <email|id>")
	}
	u, err := c.find(args[0])
	if err != nil {
		return err
	}
	roles := strings.Join(u.Roles, ", ")
	if roles == "" {
		roles = "none"
	}
	fmt.Fprintf(c.out, "  id       %s\n", u.ID)
	fmt.Fprintf(c.out, "  email    %s\n", u.Email)
	fmt.Fprintf(c.out, "  name     %s\n", u.Name)
	fmt.Fprintf(c.out, "  roles    %s\n", roles)
	fmt.Fprintf(c.out, "  verified %s\n", yesNo(u.EmailVerified))
	return nil
}

func (c *usersCmd) create(args []string) error {
	email := args[0]
	spec := tako.NewUser{Email: email, Roles: roleFlags(args[1:])}
	wantPassword := false
	flags := args[1:]
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case "--name":
			if i+1 >= len(flags) {
				return errors.New("--name needs a value")
			}
			i++
			spec.Name = flags[i]
		case "--password":
			wantPassword = true
		case "--role":
			i++ // consumed by roleFlags
		default:
			if strings.HasPrefix(flags[i], "--") {
				return fmt.Errorf("unknown flag %q (tako users create takes --name, --password, --role)", flags[i])
			}
		}
	}
	if wantPassword {
		pw, err := c.ask("  password: ", true)
		if err != nil {
			return err
		}
		if pw == "" {
			return errors.New("no password given — omit --password for an account that signs in with Google")
		}
		spec.Password = pw
	}
	users, ctx := c.users()
	u, err := users.Create(ctx, spec)
	if err != nil {
		if tako.IsEmailTaken(err) {
			return fmt.Errorf("%s already has an account — look it up with tako users show %s\n"+
				"      and grant it what it needs; an address is one account", email, email)
		}
		return c.cloudErr(err)
	}
	fmt.Fprintf(c.out, "  created  %s\n", describeUser(u))
	if !wantPassword {
		if c.passwordOnly() {
			fmt.Fprintln(c.out, "  no password set — and this app declares only email/password sign-in, so")
			fmt.Fprintln(c.out, "  this account cannot sign in yet; --password sets one")
		} else {
			fmt.Fprintln(c.out, "  no password set — this account signs in with Google; --password sets one")
		}
	}
	c.noteRegistered()
	return nil
}

// noteRegistered says what became of auth.Registered, and only when the
// app has a subscriber that would care.
//
// The publish is real against a project and absent on a laptop: this is
// the CLI, not the app, so nothing here is running the subscriber. That
// asymmetry is worth one line at the moment it matters rather than a
// paragraph in a doc nobody is reading right now.
func (c *usersCmd) noteRegistered() {
	if len(c.app.registered) == 0 {
		return
	}
	who := strings.Join(c.app.registered, ", ")
	if c.target.isCloud() {
		fmt.Fprintf(c.out, "  auth.Registered published — %s will run\n", who)
		return
	}
	fmt.Fprintf(c.out, "  auth.Registered has no subscriber here — this is the CLI, not the app;\n"+
		"  run tako dev if %s must see this account\n", who)
}

// passwordOnly reports an app whose only declared door is a password —
// where an account created without one cannot sign in at all.
func (c *usersCmd) passwordOnly() bool {
	if len(c.app.providers) == 0 {
		return false
	}
	for _, p := range c.app.providers {
		if p != "email" {
			return false
		}
	}
	return true
}

func (c *usersCmd) setRole(ref, role string, grant bool) error {
	u, err := c.find(ref)
	if err != nil {
		return err
	}
	if hasRole(u, role) == grant {
		verb := "already has"
		if !grant {
			verb = "does not have"
		}
		fmt.Fprintf(c.out, "  %s %s %s — nothing changed\n", u.Email, verb, role)
		return nil
	}
	users, ctx := c.users()
	if grant {
		err = users.Grant(ctx, u.ID, tako.Role(role))
	} else {
		err = users.Revoke(ctx, u.ID, tako.Role(role))
	}
	if err != nil {
		return c.cloudErr(err)
	}
	after, err := c.find(u.ID)
	if err != nil {
		return err
	}
	word := "granted"
	prep := "to"
	if !grant {
		word, prep = "revoked", "from"
	}
	fmt.Fprintf(c.out, "  %s  %s %s %s  %s\n", word, role, prep, u.Email, bracket(after.Roles))
	// The roles a request sees come off the session, minted before this
	// change. Saying WHEN it lands is the difference between an
	// operator who waits and one who starts debugging a 403.
	fmt.Fprintln(c.out, "  live sessions refresh on their next request — nobody is signed out")
	return nil
}

func (c *usersCmd) setEnabled(args []string, enable bool) error {
	if len(args) < 1 {
		return errors.New("this command wants an account: tako users disable|enable <email|id>")
	}
	u, err := c.find(args[0])
	if err != nil {
		return err
	}
	users, ctx := c.users()
	if enable {
		err = users.Enable(ctx, u.ID)
	} else {
		err = users.Disable(ctx, u.ID)
	}
	if err != nil {
		return c.cloudErr(err)
	}
	if enable {
		fmt.Fprintf(c.out, "  enabled  %s — sign-in allowed again\n", u.Email)
		return nil
	}
	fmt.Fprintf(c.out, "  disabled  %s — sign-in refused, record kept\n", u.Email)
	// Disabling ends the sessions, and it has to: a disabled user who
	// stays signed in for a week is not disabled.
	fmt.Fprintln(c.out, "  every session of theirs was ended")
	return nil
}

// remove is the bottom rung of the confirmation ladder — but only
// against a project.
//
// Locally, .tako/data IS the reset story: the guide tells you to delete
// the directory. Ceremony over a fixture is friction that teaches
// operators to type past prompts, which is exactly what makes the
// prompt that matters ineffective.
func (c *usersCmd) remove(args []string) error {
	if len(args) < 1 {
		return errors.New("tako users delete wants an account: tako users delete <email|id>")
	}
	u, err := c.find(args[0])
	if err != nil {
		return err
	}
	if c.target.isCloud() {
		fmt.Fprintf(c.out, "  %s\n", describeUser(u))
		fmt.Fprintln(c.out, "  this is permanent: the account is gone, and anything your app keyed on")
		fmt.Fprintln(c.out, "  its id is orphaned. tako users disable keeps the record.")
		typed, err := c.ask(fmt.Sprintf("  type the email to confirm: %s\n  > ", u.Email), false)
		if err != nil {
			return err
		}
		if strings.TrimSpace(typed) != u.Email {
			return errors.New("that is not the email — nothing was deleted")
		}
	}
	users, ctx := c.users()
	if err := users.Delete(ctx, u.ID); err != nil {
		return c.cloudErr(err)
	}
	fmt.Fprintf(c.out, "  deleted  %s  %s — every session ended\n", u.ID, u.Email)
	return nil
}

// find resolves what the operator typed. An address has an @ in it and
// an Identity Platform id does not, so the two never collide.
func (c *usersCmd) find(ref string) (tako.User, error) {
	users, ctx := c.users()
	var u tako.User
	var err error
	if strings.Contains(ref, "@") {
		u, err = users.ByEmail(ctx, ref)
	} else {
		u, err = users.Get(ctx, ref)
	}
	if err != nil {
		if tako.IsNotFound(err) {
			return u, fmt.Errorf("no account for %s — tako users list shows who there is", ref)
		}
		return u, c.cloudErr(err)
	}
	return u, nil
}

func hasRole(u tako.User, role string) bool {
	for _, r := range u.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func describeUser(u tako.User) string {
	parts := []string{u.ID, u.Email}
	if u.Name != "" {
		parts = append(parts, u.Name)
	}
	if len(u.Roles) > 0 {
		parts = append(parts, bracket(u.Roles))
	}
	return strings.Join(parts, "  ")
}

func bracket(roles []string) string {
	if len(roles) == 0 {
		return "[]"
	}
	return "[" + strings.Join(roles, ", ") + "]"
}

// roleFlags collects every --role value. Repeating the flag is how a
// new account gets two roles.
func roleFlags(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--role" && i+1 < len(args) {
			i++
			out = append(out, args[i])
			continue
		}
		if name, value, ok := strings.Cut(args[i], "="); ok && name == "--role" {
			out = append(out, value)
		}
	}
	return out
}

func writeTable(out io.Writer, rows [][]string) {
	width := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > width[i] {
				width[i] = len(cell)
			}
		}
	}
	for _, row := range rows {
		line := "  "
		for i, cell := range row {
			line += cell
			if i < len(row)-1 {
				line += strings.Repeat(" ", width[i]-len(cell)+2)
			}
		}
		fmt.Fprintln(out, strings.TrimRight(line, " "))
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
