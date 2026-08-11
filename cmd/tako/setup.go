package main

// `tako setup` and `tako datastore create` (SC2).
//
// Preflight has always found every gap and printed its fix. This is the
// half that applies them — and the half that hands the job over when it
// cannot, which is the output that matters most, because granting
// yourself the permission to grant is the one step nobody can bootstrap.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
	"github.com/gsandoval/tako/pkg/devserver"
	"github.com/gsandoval/tako/pkg/provision/gcp"
)

// errAborted is the answer to a declined prompt: the ladder's rung two,
// where the friction exists to be answered "no" sometimes.
var errAborted = fmt.Errorf("aborted — nothing was changed")

// confirmed reads one y/N.
func confirmed(prompt string) bool {
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	l := strings.ToLower(strings.TrimSpace(line))
	return l == "y" || l == "yes"
}

// runSetup is `tako setup [--project …] [--member …] [--plan] [--yes]`.
func runSetup(args []string) error {
	t, rest, err := parseTarget(args)
	if err != nil {
		return err
	}
	member := ""
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--member" && i+1 < len(rest):
			i++
			member = rest[i]
		case rest[i] == "--member":
			// Not "unknown flag": the flag is known and its VALUE is
			// missing, which is a different mistake with a different fix.
			return fmt.Errorf("--member needs a value, e.g. --member user:you@example.com " +
				"or --member serviceAccount:ci@project.iam.gserviceaccount.com")
		case strings.HasPrefix(rest[i], "--member="):
			member = strings.TrimPrefix(rest[i], "--member=")
		default:
			return fmt.Errorf("unknown flag %q (tako setup accepts --project, --region, --env, --member, --plan, --yes)", rest[i])
		}
	}

	graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	fmt.Printf("tako setup · %s\n", t.project)

	principal, self := member, false
	if principal == "" {
		account, err := gcloudAccount()
		if err != nil {
			return err
		}
		principal, self = "user:"+account, true
	}
	fmt.Printf("  granting to  %s\n", principal)
	fmt.Printf("  app          %s\n", devserver.CountLine(graph))

	client, err := gcp.NewClient(context.Background(), t.project, t.region)
	if err != nil {
		return err
	}
	ctx := context.Background()
	needs := needsOf(graph, t.env)

	// Billing before anything else. Without it every call after this
	// fails with a message about an API or a quota, and the reader has
	// no way to know they all mean one thing.
	if enabled, known := client.BillingEnabled(ctx); known && !enabled {
		return fmt.Errorf("%s has no billing account — Cloud Run, Artifact Registry and\n"+
			"      Datastore all refuse without one, and every failure after this would\n"+
			"      name something else. Link one, then re-run:\n"+
			"        https://console.cloud.google.com/billing/linkedaccount?project=%s",
			t.project, t.project)
	}

	plan, err := client.PlanSetup(ctx, needs, principal, self)
	if err != nil {
		return err
	}
	printSetupPlan(plan)

	remainder := setupRemainder(ctx, client, t, graph, needs)
	if plan.Empty() {
		fmt.Printf("  nothing to do — %d API(s) on, %d role(s) held\n", plan.HeldAPIs, plan.HeldRoles)
		printRemainder(remainder, t)
		return nil
	}
	if t.planOnly {
		fmt.Println("  --plan: nothing was changed")
		printRemainder(remainder, t)
		return nil
	}
	if !t.yes && !confirmed("  apply? [y/N] ") {
		return errAborted
	}
	say := func(format string, args ...any) { fmt.Printf("  ✓ "+format+"\n", args...) }
	if err := client.ApplySetup(ctx, plan, say); err != nil {
		return handOver(err, t.project, principal, plan)
	}
	// A brand new project could not be asked about its own IAM until
	// cloudresourcemanager was on — which just happened. Re-planning now
	// finishes the job in the run the user already approved, rather than
	// telling them to type the same command again.
	if plan.PermissionsUnknown {
		client.WaitForAPIs(ctx, plan.EnableAPIs, 90*time.Second)
		roles, err := client.PlanSetup(ctx, needs, principal, self)
		if err != nil {
			return err
		}
		if err := client.ApplySetup(ctx, roles, say); err != nil {
			return handOver(err, t.project, principal, roles)
		}
		remainder = setupRemainder(ctx, client, t, graph, needs)
	}
	printRemainder(remainder, t)
	fmt.Printf("\nNext: tako deploy --project %s\n", t.project)
	return nil
}

func printSetupPlan(plan gcp.SetupPlan) {
	if len(plan.EnableAPIs) > 0 {
		fmt.Printf("\n  APIs — %d to enable, %d already on\n", len(plan.EnableAPIs), plan.HeldAPIs)
		for _, api := range plan.EnableAPIs {
			fmt.Printf("    + %s\n", api)
		}
	}
	if len(plan.GrantRoles) > 0 {
		fmt.Printf("\n  Roles for %s — %d to grant, %d already held\n", plan.Principal, len(plan.GrantRoles), plan.HeldRoles)
		for _, role := range plan.GrantRoles {
			fmt.Printf("    + %-40s %s\n", role, plan.Reasons[role])
		}
	}
	if plan.PermissionsUnknown {
		fmt.Printf("\n  Roles for %s — not knowable yet\n", plan.Principal)
		fmt.Println("    cloudresourcemanager is off, and every IAM read goes through it.")
		fmt.Println("    It is enabled above; the roles are worked out and granted after.")
	}
	if !plan.Empty() {
		fmt.Println()
	}
}

// setupRemainder is what tako cannot do, gathered while it still has a
// client. Each entry is a sentence and the thing to run or open.
func setupRemainder(ctx context.Context, client *gcp.Client, t target, graph *analyzer.AppGraph, needs gcp.Needs) []string {
	var out []string
	if needs.Datastore && !client.DatastoreExists(ctx) {
		out = append(out, fmt.Sprintf("this app declares entities and %s has no Datastore database\n"+
			"      tako datastore create --region <region> --project %s\n"+
			"      (the location is permanent — pick the region your services run in,\n"+
			"       which is %s unless you deploy elsewhere)", t.project, t.project, t.region))
	}
	// Only the ones that are actually missing. This used to list every
	// declared secret unconditionally, so setup told an operator to
	// create a secret that already existed with a version in it — found
	// by SC4, whose whole job is knowing the difference.
	for _, name := range needs.Secrets {
		st, err := client.SecretState(ctx, name)
		if err != nil || st.Exists && st.Versions > 0 {
			continue
		}
		out = append(out, fmt.Sprintf("the secret %s has no value — that is your data, not tako's\n"+
			"      tako secrets set %s --project %s", name, declaredName(graph, t.env, name), t.project))
	}
	for _, p := range needs.AuthProviders {
		if p != "google" {
			continue
		}
		out = append(out, fmt.Sprintf("the Google sign-in provider: Google exposes no public API for\n"+
			"      creating external OAuth clients, so this one is a console click\n"+
			"      https://console.firebase.google.com/project/%s/authentication/providers\n"+
			"      → Google → Enable (pick a support email) → Save", t.project))
	}
	for _, d := range needs.Domains {
		out = append(out, fmt.Sprintf("prove you own %s, once per project — it is yours to prove\n"+
			"      gcloud domains verify %s", d, d))
	}
	return out
}

// declaredName maps a physical secret name back to what the app
// declared, since that is what `tako secrets set` takes. In prod they
// are the same; in any other env the physical one is prefixed.
//
// It rebuilds the physical name and compares, rather than matching a
// suffix (CR10). HasSuffix made the answer depend on declaration order:
// with secrets "key" and "api-key", the physical "api-key" ends with
// "key", so setup told the operator to run `tako secrets set key` for a
// gap in `api-key` — and that command succeeds, writing a value nothing
// reads while the real gap stays open.
func declaredName(graph *analyzer.AppGraph, env, physical string) string {
	for _, r := range graph.Resources {
		if r.Kind == "secret" && gcpb.SecretName(env, r.Name) == physical {
			return r.Name
		}
	}
	return physical
}

func printRemainder(remainder []string, t target) {
	if len(remainder) == 0 {
		return
	}
	fmt.Println("\nStill yours — tako cannot do these:")
	for _, r := range remainder {
		fmt.Printf("  · %s\n", r)
	}
}

// handOver turns a permission failure into the block to forward.
//
// This is the output SC2 is judged on. Somebody without
// setIamPolicy cannot be told "grant yourself the role" — that is the
// thing they cannot do — so the answer is the complete set of commands
// for whoever can, and nothing else.
func handOver(err error, project, principal string, plan gcp.SetupPlan) error {
	// ONLY a permission denial. This used to match any failure that
	// mentioned enabling APIs, and a brand new project's first error is
	// "no billing account" — so setup answered a billing problem with a
	// block of commands that would fail the same way, telling the user
	// to go and ask a colleague for a permission they already had.
	if !strings.Contains(err.Error(), "you lack permission to") {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%v\n\n      Granting yourself the permission to grant is the one step nobody can\n", err)
	fmt.Fprintf(&b, "      bootstrap, so it is somebody else's to run. Send them this — it is\n")
	fmt.Fprintf(&b, "      the whole of it:\n\n")
	if len(plan.EnableAPIs) > 0 {
		fmt.Fprintf(&b, "        gcloud services enable %s \\\n          --project %s\n\n",
			strings.Join(plan.EnableAPIs, " \\\n          "), project)
	}
	for _, role := range plan.GrantRoles {
		fmt.Fprintf(&b, "        gcloud projects add-iam-policy-binding %s \\\n          --member %s --role %s\n",
			project, principal, role)
	}
	fmt.Fprintf(&b, "\n      Then re-run tako setup to finish — it skips what is already done.")
	return fmt.Errorf("%s", b.String())
}

// runDatastore is `tako datastore create --region <r>`.
//
// Its own command because of one property: a Datastore database's
// location cannot be changed afterward. Everything else tako provisions
// is derivable, replaceable or revocable, so every other region flag in
// this CLI has a default. This one refuses to.
func runDatastore(args []string) error {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprint(os.Stderr, "tako datastore create --region <region> [--project <id>]\n\n"+
			"  Creates the project's datastore-mode database. The location is\n"+
			"  PERMANENT — a database cannot be moved later — so --region is\n"+
			"  required here and is not defaulted or inherited.\n")
		os.Exit(2)
	}
	regionGiven := false
	for _, a := range args[1:] {
		if a == "--region" || strings.HasPrefix(a, "--region=") {
			regionGiven = true
		}
	}
	// Before the project is even resolved: this refusal is the whole
	// reason the command exists, it needs nothing from the cloud, and
	// meeting "no project set" first would teach the wrong lesson.
	if !regionGiven {
		return fmt.Errorf("tako datastore create needs --region, and will not pick one for you\n" +
			"      a Datastore database's location is permanent: it cannot be moved,\n" +
			"      and every other --region in this CLI defaults because it can be\n" +
			"      changed by deploying again. Pick the region your services run in.")
	}
	t, rest, err := parseTarget(args[1:])
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unknown argument %q (tako datastore create accepts --region, --project)", rest[0])
	}
	client, err := gcp.NewClient(context.Background(), t.project, t.region)
	if err != nil {
		return err
	}
	fmt.Printf("tako datastore · %s\n", t.project)
	fmt.Printf("  creating a datastore-mode database in %s — this location is permanent\n", t.region)
	if !t.yes && !confirmed("  create? [y/N] ") {
		return errAborted
	}
	if err := client.CreateDatastore(context.Background(), t.region); err != nil {
		return err
	}
	fmt.Printf("  ✓ created\n\nNext: tako deploy --project %s\n", t.project)
	return nil
}

// gcloudAccount is who you are, per gcloud. Setup grants to a principal
// and has to name one; there is no useful default beyond this.
func gcloudAccount() (string, error) {
	out, err := exec.Command("gcloud", "config", "get-value", "account").Output()
	account := strings.TrimSpace(string(out))
	if err != nil || account == "" || account == "(unset)" {
		return "", fmt.Errorf("cannot tell who you are — gcloud has no account set. Run:\n" +
			"        gcloud auth login\n" +
			"      or name the principal yourself: tako setup --member user:you@example.com")
	}
	return account, nil
}
