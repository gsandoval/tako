package main

// SC1 tests. Transcripts and error texts are pinned exactly, because
// error text is product surface here — a message that sends an operator
// to the wrong remediation is the defect, not a cosmetic difference.
//
// The local half runs against a REAL bbolt store in a temp dir, through
// the real SDK path. There is no mock: the whole point of the design is
// that the command reuses auth.Users(), so a test that stubbed it would
// pin the one thing that cannot be allowed to drift.

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/backends"
	"github.com/gsandoval/tako/pkg/backends/local"
)

// testApp is the graph facts a journal-shaped app would produce.
func testApp() accountsApp {
	return accountsApp{
		auth:      true,
		authPos:   "app.go:12:5",
		providers: []string{"google"},
		roles:     []string{"editor", "moderator"},
	}
}

// newCmd wires a fresh local store and returns the command plus the
// buffer it prints into. Hooks are process-global, so these tests do
// not run in parallel — the same reason the runtime's boot tests don't.
func newCmd(t *testing.T, app accountsApp, answers ...string) (*usersCmd, *strings.Builder) {
	t.Helper()
	store, err := local.OpenEntity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	priorEntity, priorUsers, priorLease := hooks.Entity, hooks.Users, hooks.Lease
	hooks.Entity = store
	hooks.Users = local.NewUserStore(store)
	hooks.Lease = local.NewMemoryLease()
	t.Cleanup(func() { hooks.Entity, hooks.Users, hooks.Lease = priorEntity, priorUsers, priorLease })

	out := &strings.Builder{}
	i := 0
	return &usersCmd{
		out: out,
		app: app,
		ask: func(prompt string, _ bool) (string, error) {
			out.WriteString(prompt)
			if i < len(answers) {
				i++
				return answers[i-1], nil
			}
			return "", nil
		},
	}, out
}

func TestCreateListAndShowReadBack(t *testing.T) {
	c, out := newCmd(t, testApp())
	if err := c.run("create", []string{"ana@example.com", "--name", "Ana", "--role", "editor"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"tako users · local · .tako/data",
		"  created  ",
		"ana@example.com  Ana  [editor]",
		"  no password set — this account signs in with Google; --password sets one",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("create transcript missing %q:\n%s", want, got)
		}
	}

	c2, out2 := newCmdSharing(t, c)
	if err := c2.run("list", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), "ID") || !strings.Contains(out2.String(), "ana@example.com") {
		t.Errorf("list did not show the account just created:\n%s", out2.String())
	}
	if !strings.Contains(out2.String(), "1 account\n") {
		t.Errorf("list did not count one account:\n%s", out2.String())
	}
}

// newCmdSharing reuses the store already wired, so a second command in
// one test sees what the first one wrote.
func newCmdSharing(t *testing.T, prior *usersCmd) (*usersCmd, *strings.Builder) {
	t.Helper()
	out := &strings.Builder{}
	return &usersCmd{out: out, app: prior.app, target: prior.target, ask: prior.ask}, out
}

func TestAnEmptyStoreSaysHowToStart(t *testing.T) {
	c, out := newCmd(t, testApp())
	if err := c.run("list", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no accounts yet — tako users create <email> makes the first one") {
		t.Errorf("an empty store should say how to fill it:\n%s", out.String())
	}
}

func TestGrantSaysWhenTheRoleLands(t *testing.T) {
	c, _ := newCmd(t, testApp())
	if err := c.run("create", []string{"ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	c2, out := newCmdSharing(t, c)
	if err := c2.run("grant", []string{"ana@example.com", "moderator"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "granted  moderator to ana@example.com  [moderator]") {
		t.Errorf("grant transcript wrong:\n%s", got)
	}
	// The line that stops an operator debugging a 403 they should just
	// wait out. A grant with no word on WHEN it takes effect is why the
	// SDK's stale-marking exists and nobody knows it does.
	if !strings.Contains(got, "live sessions refresh on their next request — nobody is signed out") {
		t.Errorf("grant never said when the role lands:\n%s", got)
	}
}

func TestGrantingARoleTwiceChangesNothing(t *testing.T) {
	c, _ := newCmd(t, testApp())
	if err := c.run("create", []string{"ana@example.com", "--role", "editor"}); err != nil {
		t.Fatal(err)
	}
	c2, out := newCmdSharing(t, c)
	if err := c2.run("grant", []string{"ana@example.com", "editor"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ana@example.com already has editor — nothing changed") {
		t.Errorf("a redundant grant should say so:\n%s", out.String())
	}
}

func TestRevokeReportsWhatIsLeft(t *testing.T) {
	c, _ := newCmd(t, testApp())
	if err := c.run("create", []string{"ana@example.com", "--role", "editor", "--role", "moderator"}); err != nil {
		t.Fatal(err)
	}
	c2, out := newCmdSharing(t, c)
	if err := c2.run("revoke", []string{"ana@example.com", "editor"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "revoked  editor from ana@example.com  [moderator]") {
		t.Errorf("revoke transcript wrong:\n%s", out.String())
	}
}

func TestDisableSaysTheSessionsWentWithIt(t *testing.T) {
	c, _ := newCmd(t, testApp())
	if err := c.run("create", []string{"bruno@example.com"}); err != nil {
		t.Fatal(err)
	}
	c2, out := newCmdSharing(t, c)
	if err := c2.run("disable", []string{"bruno@example.com"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "disabled  bruno@example.com — sign-in refused, record kept") {
		t.Errorf("disable transcript wrong:\n%s", got)
	}
	// A disabled user who stays signed in for a week is not disabled;
	// the transcript is where an operator learns tako did the other half.
	if !strings.Contains(got, "every session of theirs was ended") {
		t.Errorf("disable never mentioned the sessions:\n%s", got)
	}
}

func TestAnUndeclaredRoleIsRefusedWithTheDeclaredOnes(t *testing.T) {
	c, _ := newCmd(t, testApp())
	err := c.checkRole("moderatr")
	if err == nil {
		t.Fatal("granting an undeclared role must be refused — it writes a claim no policy reads")
	}
	for _, want := range []string{
		`"moderatr" is not a role this app declares`,
		"declared: editor, moderator (tako.Role)",
		"did you mean moderator?",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q:\n%s", want, err)
		}
	}
}

func TestAnAppWithNoRolesSaysHowToDeclareOne(t *testing.T) {
	app := testApp()
	app.roles = nil
	c, _ := newCmd(t, app)
	err := c.checkRole("editor")
	if err == nil {
		t.Fatal("an app declaring no roles cannot grant one")
	}
	if !strings.Contains(err.Error(), "it declares none") ||
		!strings.Contains(err.Error(), `tako.Role("editor")`) {
		t.Errorf("refusal should name the constructor:\n%s", err)
	}
}

func TestADistantTypoGetsNoSuggestion(t *testing.T) {
	// A hint that names something unrelated is worse than no hint.
	if got := nearestRole("administrator", []string{"editor", "moderator"}); got != "" {
		t.Errorf("nearestRole suggested %q for an unrelated word", got)
	}
	if got := nearestRole("editr", []string{"editor", "moderator"}); got != "editor" {
		t.Errorf("nearestRole(editr) = %q, want editor", got)
	}
}

func TestAMissingAccountSaysWhereToLook(t *testing.T) {
	c, _ := newCmd(t, testApp())
	_, err := c.find("nobody@example.com")
	if err == nil {
		t.Fatal("finding an absent account must fail")
	}
	if !strings.Contains(err.Error(), "no account for nobody@example.com — tako users list shows who there is") {
		t.Errorf("wrong absence message: %v", err)
	}
}

func TestATakenAddressSendsYouToTheAccount(t *testing.T) {
	c, _ := newCmd(t, testApp())
	if err := c.run("create", []string{"ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	c2, _ := newCmdSharing(t, c)
	err := c2.run("create", []string{"ana@example.com"})
	if err == nil {
		t.Fatal("a second account on one address must be refused")
	}
	// The remedy is to look it up and grant it what it needs, never to
	// retry — so the message says that rather than reporting a conflict.
	if !strings.Contains(err.Error(), "already has an account — look it up with tako users show ana@example.com") {
		t.Errorf("wrong duplicate message: %v", err)
	}
}

func TestAPasswordOnlyAppWarnsThatTheAccountCannotSignIn(t *testing.T) {
	app := testApp()
	app.providers = []string{"email"}
	c, out := newCmd(t, app)
	if err := c.run("create", []string{"ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "this app declares only email/password sign-in, so") {
		t.Errorf("an account with no password and no other door is unusable, and unremarked:\n%s", out.String())
	}
}

func TestCreateWithAPasswordPromptsForIt(t *testing.T) {
	c, out := newCmd(t, testApp(), "hunter2")
	if err := c.run("create", []string{"ana@example.com", "--password"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "  password: ") {
		t.Errorf("--password did not prompt:\n%s", out.String())
	}
	if strings.Contains(out.String(), "no password set") {
		t.Errorf("an account created WITH a password was told it has none:\n%s", out.String())
	}
}

// --- D1: the target is never inferred ---------------------------------

func TestNoProjectMeansLocalEvenWithAProjectInTheEnvironment(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "someone-elses-prod")
	target, rest, err := parseAccountsTarget([]string{"ana@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if target.isCloud() {
		t.Fatalf("GOOGLE_CLOUD_PROJECT made tako users address %q — a dev fixture would land in production", target.project)
	}
	if target.describe() != "local · .tako/data" {
		t.Errorf("target described as %q", target.describe())
	}
	if len(rest) != 1 || rest[0] != "ana@example.com" {
		t.Errorf("positional args mangled: %q", rest)
	}
}

func TestEnvWithoutProjectIsRefusedAndExplainsWhy(t *testing.T) {
	_, _, err := parseAccountsTarget([]string{"--env", "staging"})
	if err == nil {
		t.Fatal("--env alone names a cloud environment with no cloud to look in")
	}
	for _, want := range []string{
		"--env names a cloud environment but no project was given",
		"add --project <id>, or drop --env to work on .tako/data",
		"never takes the project from GOOGLE_CLOUD_PROJECT",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q:\n%s", want, err)
		}
	}
}

func TestAProjectMakesItCloudAndPrintsTheTarget(t *testing.T) {
	target, rest, err := parseAccountsTarget([]string{"--project=acme-prod-4821", "ana@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !target.isCloud() {
		t.Fatal("--project must select the cloud")
	}
	if target.env != "prod" {
		t.Errorf("env defaulted to %q, want prod", target.env)
	}
	if got := target.describe(); got != "cloud · acme-prod-4821 / prod" {
		t.Errorf("target line = %q", got)
	}
	if len(rest) != 1 || rest[0] != "ana@example.com" {
		t.Errorf("positional args mangled: %q", rest)
	}
}

func TestDeletingInTheCloudDemandsTheEmailAndLocallyDoesNot(t *testing.T) {
	c, _ := newCmd(t, testApp())
	if err := c.run("create", []string{"bruno@example.com"}); err != nil {
		t.Fatal(err)
	}

	// Cloud: the wrong answer deletes nothing.
	cloud, _ := newCmdSharing(t, c)
	cloud.target = accountsTarget{project: "acme-prod-4821", env: "prod"}
	cloud.ask = func(string, bool) (string, error) { return "yes", nil }
	if err := cloud.run("delete", []string{"bruno@example.com"}); err == nil {
		t.Fatal("a mistyped confirmation must not delete")
	} else if !strings.Contains(err.Error(), "that is not the email — nothing was deleted") {
		t.Errorf("wrong refusal: %v", err)
	}
	if _, err := c.find("bruno@example.com"); err != nil {
		t.Fatalf("the account was deleted despite the refusal: %v", err)
	}

	// Local: .tako/data is the reset story, and ceremony over a fixture
	// teaches operators to type past prompts.
	plain, out := newCmdSharing(t, c)
	if err := plain.run("delete", []string{"bruno@example.com"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "type the email") {
		t.Errorf("a local delete should not hold a ceremony:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "every session ended") {
		t.Errorf("delete transcript wrong:\n%s", out.String())
	}
}

// --- the refusals an operator actually meets --------------------------

// Four cloud failures wear the same HTTP clothes and have four
// different remediations. Handing over a status code makes the operator
// guess, and the guesses are expensive: "enable an API" and "grant
// yourself a role" and "deploy once" are not interchangeable.
func TestEachCloudRefusalNamesItsOwnRemedy(t *testing.T) {
	c := &usersCmd{target: accountsTarget{project: "acme-prod-4821", env: "prod"}}
	for _, tc := range []struct{ raw, want string }{
		{"HTTP 403: SERVICE_DISABLED for identitytoolkit", "gcloud services enable identitytoolkit.googleapis.com --project acme-prod-4821"},
		{"HTTP 400: CONFIGURATION_NOT_FOUND", "Identity Platform is not initialized on acme-prod-4821"},
		{"HTTP 403 from identitytoolkit: caller lacks permission", "--role roles/identitytoolkit.admin"},
		{"HTTP 401 from identitytoolkit", "gcloud auth application-default login"},
		// FL22: two more that wear a 403 and are not about a role at
		// all. The first is what a credential without
		// serviceusage.services.use meets now that tako sends the
		// billing header; the second is what identitytoolkit answered
		// before it sent one. Both used to be reported as a missing
		// identitytoolkit.admin, which is a grant that changes nothing.
		{"HTTP 403: Permission denied on serviceusage.services.use", "--role roles/serviceusage.serviceUsageConsumer"},
		{"HTTP 403: The identitytoolkit.googleapis.com API requires a quota project, which is not set by default.",
			"needs a billing project on acme-prod-4821"},
	} {
		got := c.cloudErr(errors.New(tc.raw))
		if got == nil || !strings.Contains(got.Error(), tc.want) {
			t.Errorf("%q →\n%v\nwant it to mention %q", tc.raw, got, tc.want)
		}
	}
	// Anything unrecognised is passed through: a wrong guess about a
	// failure is worse than the raw text.
	if got := c.cloudErr(errors.New("connection reset")); got.Error() != "connection reset" {
		t.Errorf("an unclassified error was reinterpreted as %v", got)
	}
}

// The one an operator meets on the first day, in both directions: the
// dev server holds .tako/data, and the store's own message blames an
// orphan.
func TestTheLockedLocalStoreBlamesTheDevServer(t *testing.T) {
	c := &usersCmd{}
	err := c.cloudErr(errors.New("opening local entity store: another process holds .tako/data/entity.db"))
	if err == nil {
		t.Fatal("a locked store must still be an error")
	}
	for _, want := range []string{"`tako dev` holds", "sign in through the", "lsof .tako/data/entity.db"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("lock message missing %q:\n%s", want, err)
		}
	}
}

// --- the graph half ---------------------------------------------------

func TestTheGraphAnswersBeforeAnyCredentialIsTouched(t *testing.T) {
	graph := &analyzer.AppGraph{
		Module: "example.com/shop",
		Roles:  []string{"admin"},
		Resources: []analyzer.Resource{
			{Kind: "auth", Providers: []string{"email"}, Pos: "app.go:9:5"},
			{Kind: "entity", Name: "orders"},
		},
		Handlers: []analyzer.Handler{
			{Kind: "subscribe", Var: "shop/mail.WelcomeMail", Binding: hooks.AuthRegisteredSource},
			{Kind: "subscribe", Var: "shop.Reindex", Binding: "photos"},
		},
	}
	app := accountsAppFrom(graph)
	if !app.auth || app.authPos != "app.go:9:5" {
		t.Errorf("auth not seen: %+v", app)
	}
	if len(app.roles) != 1 || app.roles[0] != "admin" {
		t.Errorf("roles = %q", app.roles)
	}
	if len(app.registered) != 1 || app.registered[0] != "WelcomeMail" {
		t.Errorf("auth.Registered subscribers = %q, want [WelcomeMail]", app.registered)
	}
}

// The constant is spelled in users.go and owned by internal/hooks. If
// they ever disagree, every "who is listening" line goes quietly blank.
func TestTheRegisteredTopicNameMatchesTheRuntimes(t *testing.T) {
	if authRegisteredSource != hooks.AuthRegisteredSource {
		t.Fatalf("users.go says %q, the runtime publishes to %q — the subscriber note would never fire",
			authRegisteredSource, hooks.AuthRegisteredSource)
	}
}

func TestCreateNamesTheSubscriberThatWillNotSeeItLocally(t *testing.T) {
	app := testApp()
	app.registered = []string{"WelcomeMail"}
	c, out := newCmd(t, app)
	if err := c.run("create", []string{"ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auth.Registered has no subscriber here — this is the CLI, not the app") ||
		!strings.Contains(out.String(), "run tako dev if WelcomeMail must see this account") {
		t.Errorf("local create should say the subscriber will not run:\n%s", out.String())
	}

	cloud, cloudOut := newCmdSharing(t, c)
	cloud.target = accountsTarget{project: "acme-prod-4821", env: "prod"}
	if err := cloud.run("create", []string{"bruno@example.com"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cloudOut.String(), "auth.Registered published — WelcomeMail will run") {
		t.Errorf("a cloud create publishes for real and should say so:\n%s", cloudOut.String())
	}
}

func TestAnAppWithNoSubscriberSaysNothingAboutEvents(t *testing.T) {
	c, out := newCmd(t, testApp())
	if err := c.run("create", []string{"ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "auth.Registered") {
		t.Errorf("nothing subscribes; the line is noise:\n%s", out.String())
	}
}

// --- D3: the command may not reach past the SDK -----------------------

// The whole design is that `tako users` calls auth.Users() and the SDK
// does the compound act — write the account, mark its sessions stale,
// under a lease. A command that reached the store directly would
// eventually do half of that, and the half it forgot would be a role
// that never landed or a disabled user still signed in for a week.
//
// Wiring needs the backend packages; the COMMAND does not. So the two
// live in separate files and this pins the boundary.
func TestTheUsersCommandNeverReachesPastTheSDK(t *testing.T) {
	// Imports, not text: a comment that NAMES internal/hooks (there is
	// one, explaining why a constant is spelled twice) is documentation,
	// and a guard that cannot tell the two apart gets worked around by
	// rewording rather than by fixing.
	file, err := parser.ParseFile(token.NewFileSet(), "users.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if strings.Contains(path, "pkg/backends") || strings.Contains(path, "internal/hooks") {
			t.Errorf("users.go imports %s — account operations belong to auth.Users(), and a "+
				"second copy of its orchestration is one nobody will remember to update", path)
		}
	}
}

// Because the command goes through the SDK, the compound acts it
// depends on are already pinned where they belong —
// pkg/runtime/staleroles_test.go proves a role change lands on the next
// request and that disabling ends the session. What is NOT covered
// there is this command's own wiring: the SDK marks sessions stale
// through hooks.Entity, so an openAccounts that wired only the accounts
// would change a role and leave every signed-in browser on the old one
// until it expired — with no error anywhere.
func TestWiringTheAccountsAlsoWiresWhatSessionsNeed(t *testing.T) {
	t.Chdir(t.TempDir())
	prior := struct{ entity, users, lease any }{hooks.Entity, hooks.Users, hooks.Lease}
	t.Cleanup(func() {
		hooks.Entity, _ = prior.entity.(backends.Entity)
		hooks.Users, _ = prior.users.(backends.Users)
		hooks.Lease, _ = prior.lease.(backends.Lease)
	})
	hooks.Entity, hooks.Users, hooks.Lease = nil, nil, nil

	closeAccounts, err := openAccounts(accountsTarget{}, &analyzer.AppGraph{Module: "example.com/app"})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAccounts()

	if hooks.Users == nil {
		t.Error("no accounts wired — every command would report them unwired")
	}
	if hooks.Entity == nil {
		t.Error("no entity store wired — a role change would never reach a live session, " +
			"and nothing would say so")
	}
	if hooks.Lease == nil {
		t.Error("no lease wired — Grant and Revoke are read-modify-write over one claims blob, " +
			"so two at once lose one silently")
	}
	// The accounts `tako users create` makes must be the accounts `tako
	// dev` signs in, or this is a fixture factory for a database nobody
	// reads. .tako/data is where runtime.BootLocal keeps them — proved
	// by writing one and finding the file, since the local store opens
	// its directory lazily and an unused command litters nothing.
	c := &usersCmd{out: &strings.Builder{}, app: testApp()}
	if err := c.run("create", []string{"ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(".tako", "data", "entity.db")); err != nil {
		t.Errorf("an account created by the CLI did not land in .tako/data, where tako dev reads: %v", err)
	}
}
