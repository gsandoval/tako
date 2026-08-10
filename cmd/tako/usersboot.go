package main

// Wiring for `tako users` (SC1), kept out of users.go on purpose.
//
// This is the only file in the command that names a backend. Everything
// the operator asked for is done through auth.Users(), which reads
// whatever is wired here — so the two halves are "which accounts" and
// "what to do with them", and neither knows the other's business. The
// split is not tidiness: a command that can reach the store directly
// will eventually do a two-step act (change a role, then mark the
// sessions) in one place while the SDK does it in another, and the two
// copies will drift. A test asserts users.go imports no backend.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/analyzer"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
	"github.com/gsandoval/tako/pkg/backends/local"
	"github.com/gsandoval/tako/pkg/provision"
)

// defaultAccountsEnv is the environment a --project run addresses when
// --env is not given — the same default every other cloud command uses.
const defaultAccountsEnv = provision.DefaultEnv

// openAccounts wires the backends auth.Users() reads, and returns the
// closer.
//
// Two of them, always: the accounts themselves, and the ENTITY store,
// because a role change marks sessions stale and disabling ends them —
// and sessions are entities. Wiring the accounts alone would produce a
// command that changes a role and silently leaves every signed-in
// browser on the old one until it expires.
//
// The lease is the third: Grant and Revoke are read-modify-write over a
// claims blob written whole, so the SDK takes a lease keyed on the
// account. An unwired lease is not an error (the SDK falls back), but
// it is a lost update waiting for the moment an operator and a running
// service change the same person at once.
func openAccounts(t accountsTarget, graph *analyzer.AppGraph) (func(), error) {
	if t.isCloud() {
		return openCloudAccounts(t, graph)
	}
	dir := filepath.Join(".tako", "data")
	store, err := local.OpenEntity(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot open the local accounts in %s: %v", dir, err)
	}
	hooks.Entity = store
	hooks.Users = local.NewUserStore(store)
	hooks.Lease = local.NewMemoryLease()
	hooks.LeaseOwner = "tako-users-cli"
	return func() { _ = store.Close() }, nil
}

func openCloudAccounts(t accountsTarget, graph *analyzer.AppGraph) (func(), error) {
	cfg := provision.Config{Project: t.project, App: graph.Module, Env: t.env}
	// The SAME identity the deployed services use: AppID off the module
	// path, env as the Datastore namespace. Anything else would address
	// a different set of sessions than the app writes.
	c, err := gcpb.New(context.Background(), gcpb.Options{
		Project: t.project,
		App:     cfg.AppID(),
		Env:     t.env,
	})
	if err != nil {
		return nil, fmt.Errorf("not authenticated to Google Cloud — run:\n"+
			"        gcloud auth application-default login\n\n      (%v)", err)
	}
	hooks.Entity = gcpb.NewEntityStore(c)
	hooks.Users = gcpb.NewUserStore(c)
	hooks.Lease = gcpb.NewLeaseStore(c)
	hooks.LeaseOwner = "tako-users-cli"
	return func() {}, nil
}

// cloudErr turns an API refusal into the one thing the operator can act
// on. Four failures wear the same HTTP clothes and have four different
// remediations, so they are told apart here rather than handed over as
// a status code:
//
//   - the API is off      → this app's sign-in was never deployed here
//   - the caller lacks IAM→ one grant, printed
//   - no IP configuration → deploy once; the deploy initializes it
//   - anything else       → unchanged, because a guess is worse
//
// The permission one is the reason SC1 proposes adding
// identitytoolkit.admin to the Layer 1 deployer bundle: today it is
// only in Layer 2 (derived, per service), so an operator holding the
// full documented setup meets a 403 here.
func (c *usersCmd) cloudErr(err error) error {
	if err == nil {
		return err
	}
	msg := err.Error()
	// The local one, first, because it is the one an operator meets on
	// the first day. bbolt takes an exclusive lock, so `tako users`
	// cannot open .tako/data while `tako dev` is running — and the
	// store's own message guesses at "an orphaned service from a
	// previous run", which sends the reader hunting for a zombie when
	// the holder is the terminal next to this one.
	if strings.Contains(msg, "another process holds") {
		return errors.New("the local accounts are open in another process — `tako dev` holds\n" +
			"      .tako/data while it runs. Stop it and re-run, or sign in through the\n" +
			"      dev bar, which creates the account inside the running app.\n\n" +
			"      (if nothing is running, an earlier dev process was orphaned: find it\n" +
			"      with `lsof .tako/data/entity.db`)")
	}
	if !c.target.isCloud() {
		return err
	}
	switch {
	case strings.Contains(msg, "SERVICE_DISABLED") || strings.Contains(msg, "has not been used in project"):
		return fmt.Errorf("identitytoolkit.googleapis.com is disabled on %s — this app's sign-in\n"+
			"      has never been deployed there. Run tako deploy (it initializes Identity\n"+
			"      Platform), or enable the API alone:\n"+
			"        gcloud services enable identitytoolkit.googleapis.com --project %s",
			c.target.project, c.target.project)
	case strings.Contains(msg, "CONFIGURATION_NOT_FOUND"):
		return fmt.Errorf("Identity Platform is not initialized on %s — tako deploy initializes it\n"+
			"      when the app declares tako.Auth. Deploy once, then re-run.", c.target.project)
	case strings.Contains(msg, "HTTP 403") || strings.Contains(msg, "permission denied"):
		return fmt.Errorf("you lack identitytoolkit.admin on %s — grant it once:\n"+
			"        gcloud projects add-iam-policy-binding %s \\\n"+
			"          --member user:$(gcloud config get account) --role roles/identitytoolkit.admin",
			c.target.project, c.target.project)
	case strings.Contains(msg, "HTTP 401"):
		return errors.New("not authenticated to Google Cloud — run:\n" +
			"        gcloud auth application-default login")
	}
	return err
}

// askTerminal reads one line from the operator. A password is read with
// echo off when there is a terminal to turn it off on; piped input (a
// script, a test) reads a line and says nothing, because a prompt that
// demands a TTY is a command CI cannot run.
func askTerminal(prompt string, secret bool) (string, error) {
	fmt.Fprint(os.Stdout, prompt)
	if secret && term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stdout)
		return string(raw), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	// Piped input echoes nothing, so the newline the operator's Enter
	// would have produced has to come from here — without it the next
	// line of the transcript continues the prompt ("password:   created").
	fmt.Fprintln(os.Stdout)
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
