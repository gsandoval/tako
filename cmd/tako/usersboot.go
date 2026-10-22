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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/backends"
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
	if client, ok := tryLiveDevClient(dir); ok {
		prevStale, prevEnd := hooks.StaleSessions, hooks.EndSessions
		hooks.Users = client
		hooks.Lease = local.NewMemoryLease()
		hooks.LeaseOwner = "tako-users-cli"
		hooks.StaleSessions = func(context.Context, string) error { return nil }
		hooks.EndSessions = func(context.Context, string) error { return nil }
		return func() {
			hooks.StaleSessions = prevStale
			hooks.EndSessions = prevEnd
		}, nil
	}
	store, err := local.OpenEntity(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot open the local accounts in %s: %v", dir, err)
	}
	hooks.Entity = store
	hooks.Users = &localOrDevUsers{local: local.NewUserStore(store), dir: dir}
	hooks.Lease = local.NewMemoryLease()
	hooks.LeaseOwner = "tako-users-cli"
	return func() { _ = store.Close() }, nil
}

type devServerInfo struct {
	URL   string `json:"url"`
	Nonce string `json:"nonce"`
}

var devUsersTransport http.RoundTripper

func tryLiveDevClient(dir string) (*devUsersClient, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "dev.json"))
	if err != nil {
		return nil, false
	}
	var info devServerInfo
	if err := json.Unmarshal(raw, &info); err != nil || info.URL == "" || info.Nonce == "" {
		return nil, false
	}
	hc := &http.Client{Timeout: 500 * time.Millisecond, Transport: devUsersTransport}
	res, err := hc.Get(strings.TrimRight(info.URL, "/") + "/_tako/dev/ready")
	if err != nil {
		return nil, false
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != info.Nonce {
		return nil, false
	}
	return &devUsersClient{
		endpoint: strings.TrimRight(info.URL, "/") + "/_tako/dev/users",
		nonce:    info.Nonce,
		client:   &http.Client{Timeout: 5 * time.Second, Transport: devUsersTransport},
	}, true
}

type localOrDevUsers struct {
	local *local.UserStore
	dir   string
}

func (u *localOrDevUsers) delegateOnLock(err error) (*devUsersClient, bool) {
	if err != nil && strings.Contains(err.Error(), "another process holds") {
		if c, ok := tryLiveDevClient(u.dir); ok {
			hooks.StaleSessions = func(context.Context, string) error { return nil }
			hooks.EndSessions = func(context.Context, string) error { return nil }
			return c, true
		}
	}
	return nil, false
}

func (u *localOrDevUsers) Create(ctx context.Context, spec backends.NewUserSpec) (backends.UserRecord, error) {
	rec, err := u.local.Create(ctx, spec)
	if c, ok := u.delegateOnLock(err); ok {
		return c.Create(ctx, spec)
	}
	return rec, err
}

func (u *localOrDevUsers) Get(ctx context.Context, id string) (backends.UserRecord, error) {
	rec, err := u.local.Get(ctx, id)
	if c, ok := u.delegateOnLock(err); ok {
		return c.Get(ctx, id)
	}
	return rec, err
}

func (u *localOrDevUsers) ByEmail(ctx context.Context, email string) (backends.UserRecord, error) {
	rec, err := u.local.ByEmail(ctx, email)
	if c, ok := u.delegateOnLock(err); ok {
		return c.ByEmail(ctx, email)
	}
	return rec, err
}

func (u *localOrDevUsers) List(ctx context.Context, limit int, after string) ([]backends.UserRecord, string, error) {
	list, next, err := u.local.List(ctx, limit, after)
	if c, ok := u.delegateOnLock(err); ok {
		return c.List(ctx, limit, after)
	}
	return list, next, err
}

func (u *localOrDevUsers) SetRoles(ctx context.Context, id string, roles []string) error {
	err := u.local.SetRoles(ctx, id, roles)
	if c, ok := u.delegateOnLock(err); ok {
		return c.SetRoles(ctx, id, roles)
	}
	return err
}

func (u *localOrDevUsers) SetDisabled(ctx context.Context, id string, disabled bool) error {
	err := u.local.SetDisabled(ctx, id, disabled)
	if c, ok := u.delegateOnLock(err); ok {
		return c.SetDisabled(ctx, id, disabled)
	}
	return err
}

func (u *localOrDevUsers) Delete(ctx context.Context, id string) error {
	err := u.local.Delete(ctx, id)
	if c, ok := u.delegateOnLock(err); ok {
		return c.Delete(ctx, id)
	}
	return err
}

type devUsersClient struct {
	endpoint string
	nonce    string
	client   *http.Client
}

type devUsersPayload struct {
	Action   string   `json:"action"`
	ID       string   `json:"id,omitempty"`
	Email    string   `json:"email,omitempty"`
	Password string   `json:"password,omitempty"`
	Name     string   `json:"name,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	After    string   `json:"after,omitempty"`
}

type devUsersReply struct {
	User  *backends.UserRecord  `json:"user,omitempty"`
	Users []backends.UserRecord `json:"users,omitempty"`
	Next  string                `json:"next,omitempty"`
	Error string                `json:"error,omitempty"`
	Code  string                `json:"code,omitempty"`
}

func (c *devUsersClient) call(ctx context.Context, req devUsersPayload) (devUsersReply, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return devUsersReply{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return devUsersReply{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Tako-Dev-Nonce", c.nonce)
	res, err := c.client.Do(httpReq)
	if err != nil {
		return devUsersReply{}, err
	}
	defer res.Body.Close()
	var reply devUsersReply
	_ = json.NewDecoder(res.Body).Decode(&reply)
	if res.StatusCode >= 400 {
		switch reply.Code {
		case "not_found":
			return devUsersReply{}, backends.ErrNotFound
		case "email_taken":
			return devUsersReply{}, backends.ErrEmailTaken
		case "roles_too_large":
			return devUsersReply{}, errors.New(reply.Error)
		default:
			if reply.Error != "" {
				return devUsersReply{}, errors.New(reply.Error)
			}
			return devUsersReply{}, fmt.Errorf("dev users endpoint returned HTTP %d", res.StatusCode)
		}
	}
	return reply, nil
}

func (c *devUsersClient) Create(ctx context.Context, spec backends.NewUserSpec) (backends.UserRecord, error) {
	reply, err := c.call(ctx, devUsersPayload{
		Action:   "create",
		ID:       spec.ID,
		Email:    spec.Email,
		Password: spec.Password,
		Name:     spec.Name,
		Roles:    spec.Roles,
	})
	if err != nil || reply.User == nil {
		return backends.UserRecord{}, err
	}
	return *reply.User, nil
}

func (c *devUsersClient) Get(ctx context.Context, id string) (backends.UserRecord, error) {
	reply, err := c.call(ctx, devUsersPayload{Action: "get", ID: id})
	if err != nil || reply.User == nil {
		return backends.UserRecord{}, err
	}
	return *reply.User, nil
}

func (c *devUsersClient) ByEmail(ctx context.Context, email string) (backends.UserRecord, error) {
	reply, err := c.call(ctx, devUsersPayload{Action: "by_email", Email: email})
	if err != nil || reply.User == nil {
		return backends.UserRecord{}, err
	}
	return *reply.User, nil
}

func (c *devUsersClient) List(ctx context.Context, limit int, after string) ([]backends.UserRecord, string, error) {
	reply, err := c.call(ctx, devUsersPayload{Action: "list", Limit: limit, After: after})
	if err != nil {
		return nil, "", err
	}
	return reply.Users, reply.Next, nil
}

func (c *devUsersClient) SetRoles(ctx context.Context, id string, roles []string) error {
	_, err := c.call(ctx, devUsersPayload{Action: "set_roles", ID: id, Roles: roles})
	return err
}

func (c *devUsersClient) SetDisabled(ctx context.Context, id string, disabled bool) error {
	_, err := c.call(ctx, devUsersPayload{Action: "set_disabled", ID: id, Disabled: disabled})
	return err
}

func (c *devUsersClient) Delete(ctx context.Context, id string) error {
	_, err := c.call(ctx, devUsersPayload{Action: "delete", ID: id})
	return err
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
	// Billing attribution, not permission — and it must be told apart
	// from the role case below, which is the mistake this command used to
	// make: it named a grant that changes nothing (FL22).
	case strings.Contains(msg, "serviceusage.services.use"):
		return fmt.Errorf("cannot bill identitytoolkit calls to %s — grant:\n"+
			"        gcloud projects add-iam-policy-binding %s \\\n"+
			"          --member user:$(gcloud config get account) \\\n"+
			"          --role roles/serviceusage.serviceUsageConsumer",
			c.target.project, c.target.project)
	case strings.Contains(msg, "quota project"):
		return fmt.Errorf("identitytoolkit.googleapis.com needs a billing project on %s and this\n"+
			"      credential carries none — tako sends one for user credentials from\n"+
			"      `gcloud auth application-default login`. On a service-account key file,\n"+
			"      use ADC instead, or set GOOGLE_CLOUD_QUOTA_PROJECT.", c.target.project)
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

// askTerminal reads a value from the operator. A password is read with
// echo off when there is a terminal to turn it off on; piped input (a
// script, a test) is read whole and says nothing, because a prompt that
// demands a TTY is a command CI cannot run.
func askTerminal(prompt string, secret bool) (string, error) {
	fmt.Fprint(os.Stdout, prompt)
	if secret && term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stdout)
		return string(raw), err
	}
	value, err := readAll(os.Stdin)
	// Piped input echoes nothing, so the newline the operator's Enter
	// would have produced has to come from here — without it the next
	// line of the transcript continues the prompt ("password:   created").
	fmt.Fprintln(os.Stdout)
	return value, err
}

// readAll takes the WHOLE of a piped value, not its first line (CR9).
//
// It used to be ReadString('\n'), which is right for a typed password
// and silently wrong for `cat key.pem | tako secrets set signing-key`:
// the stored value was "-----BEGIN PRIVATE KEY-----" and the command
// said ✓. The reference documents stdin as the input, so the piped path
// is the supported one, and it was the one that lost data.
//
// Exactly one trailing newline comes off — that is the operator's
// Enter, not part of the value. Anything beyond it belongs to whoever
// sent it.
func readAll(r io.Reader) (string, error) {
	raw, err := io.ReadAll(r)
	if err != nil && len(raw) == 0 {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r"), nil
}
