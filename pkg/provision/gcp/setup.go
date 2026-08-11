package gcp

// `tako setup` (SC2): applying what preflight already knows how to find.
//
// Preflight has always derived exactly what an app's graph needs and
// printed a copy-pasteable fix for every gap. Nobody ever applied it, so
// a first deploy meant transcribing enable-lines and role-bindings out
// of a table by hand, one mistake at a time. This is the other half.
//
// Two things it deliberately does NOT do. It never creates the
// Datastore database — that location is permanent, so it is its own
// command with a required --region. And it never grants more than the
// graph asks for: the owner's ruling (2026-08-10) is the tighter set
// always, which means setup is a command you re-run as the app grows,
// and the preflight gap that prompts the re-run names it.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// SetupPlan is what setup would do, and is printed before anything
// happens. An empty plan is the normal state of a converged project.
type SetupPlan struct {
	// Principal is who the roles are granted to, in IAM's own spelling
	// ("user:ana@example.com", "serviceAccount:ci@p.iam.gserviceaccount.com").
	Principal string
	// EnableAPIs are the services to turn on, already filtered to the
	// disabled ones.
	EnableAPIs []string
	// GrantRoles are the roles to add, deduped, in a stable order.
	GrantRoles []string
	// HeldAPIs and HeldRoles are counts for the "already done" line —
	// the difference between "nothing to do" and "nothing happened".
	HeldAPIs, HeldRoles int
	// PermissionsUnknown is set when the roles could not be checked at
	// all, because cloudresourcemanager — the API every IAM read goes
	// through — is itself disabled. That is the state of a brand new
	// project, and it is reported rather than guessed at: the plan says
	// the API comes first and the roles are worked out after it is on.
	PermissionsUnknown bool
	// Reasons maps each granted role to the permission that demanded it,
	// so the plan can say why rather than only what.
	Reasons map[string]string
}

// Empty reports a converged project. A plan that could not check
// permissions is never empty: there is at least an API to turn on
// before anything can be known.
func (p SetupPlan) Empty() bool {
	return len(p.EnableAPIs) == 0 && len(p.GrantRoles) == 0 && !p.PermissionsUnknown
}

// PlanSetup reads the project and works out the difference between what
// this graph needs and what is already true. It mutates nothing.
//
// self says the principal IS the caller, which decides how "already
// held" is answered: testIamPermissions asks about the caller and
// nobody else, so granting to a CI service account has to read the
// policy instead.
func (c *Client) PlanSetup(ctx context.Context, needs Needs, principal string, self bool) (SetupPlan, error) {
	apis, perms := Requirements(needs)
	// cloudbilling is setup's own: without it the billing check cannot
	// answer, and "no billing account" is the failure that makes every
	// later error name the wrong thing.
	apis = append(apis, billingHost)
	plan := SetupPlan{Principal: principal, Reasons: map[string]string{}}

	for _, api := range apis {
		var state struct {
			State string `json:"state"`
		}
		url := c.url(serviceuseHost, "/v1/projects/%s/services/%s", c.Project, api)
		if err := c.do(ctx, http.MethodGet, url, nil, &state); err != nil {
			return plan, Classify(err, c.Project, "read the project's enabled APIs", "roles/serviceusage.serviceUsageViewer")
		}
		if state.State == "ENABLED" {
			plan.HeldAPIs++
			continue
		}
		plan.EnableAPIs = append(plan.EnableAPIs, api)
	}

	// Every IAM read goes through cloudresourcemanager, and a brand new
	// project does not have it. Asking anyway produces "the
	// cloudresourcemanager API is disabled" — true, unhelpful, and the
	// end of a command whose job is to fix exactly that. So the roles
	// wait for the API, and the plan says so.
	for _, api := range plan.EnableAPIs {
		if api == crmHost {
			plan.PermissionsUnknown = true
			return plan, nil
		}
	}

	// testIamPermissions answers for the CALLER, which is right when
	// setup is granting to itself and wrong when it is granting to a CI
	// service account. For another principal the policy is read
	// directly, because "can I do this" is not the question.
	var held map[string]bool
	var err error
	if self {
		held, err = c.permissionsHeld(ctx, perms)
	} else {
		held, err = c.rolesHeldBy(ctx, principal)
	}
	if err != nil {
		return plan, err
	}

	seen := map[string]bool{}
	for _, p := range perms {
		if seen[p.Role] {
			continue
		}
		// held is keyed by permission when we asked about ourselves and
		// by role when we read the policy; either answers "is this
		// role's work already possible".
		if held[p.Name] || held[p.Role] {
			seen[p.Role] = true
			plan.HeldRoles++
			continue
		}
		seen[p.Role] = true
		plan.GrantRoles = append(plan.GrantRoles, p.Role)
		plan.Reasons[p.Role] = p.Name
	}
	return plan, nil
}

func (c *Client) permissionsHeld(ctx context.Context, perms []Perm) (map[string]bool, error) {
	ask := make([]string, len(perms))
	for i, p := range perms {
		ask[i] = p.Name
	}
	var granted struct {
		Permissions []string `json:"permissions"`
	}
	url := c.url(crmHost, "/v1/projects/%s:testIamPermissions", c.Project)
	if err := c.do(ctx, http.MethodPost, url, map[string]any{"permissions": ask}, &granted); err != nil {
		return nil, Classify(err, c.Project, "check permissions", "roles/viewer")
	}
	have := map[string]bool{}
	for _, p := range granted.Permissions {
		have[p] = true
	}
	return have, nil
}

func (c *Client) rolesHeldBy(ctx context.Context, principal string) (map[string]bool, error) {
	policy, err := c.projectPolicy(ctx)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, b := range policy.Bindings {
		for _, m := range b.Members {
			if m == principal {
				have[b.Role] = true
			}
		}
	}
	return have, nil
}

type iamPolicy struct {
	Bindings []struct {
		Role    string   `json:"role"`
		Members []string `json:"members"`
	} `json:"bindings"`
	Etag    string `json:"etag"`
	Version int    `json:"version"`
}

func (c *Client) projectPolicy(ctx context.Context) (*iamPolicy, error) {
	var policy iamPolicy
	url := c.url(crmHost, "/v1/projects/%s:getIamPolicy", c.Project)
	if err := c.do(ctx, http.MethodPost, url, map[string]any{}, &policy); err != nil {
		return nil, Classify(err, c.Project, "read the project's IAM policy", "roles/resourcemanager.projectIamAdmin")
	}
	return &policy, nil
}

// ApplySetup enables and grants, narrating as it goes. APIs first: a
// grant against a project whose API is off fails in a way that reads
// like a permission problem.
func (c *Client) ApplySetup(ctx context.Context, plan SetupPlan, say func(format string, args ...any)) error {
	if len(plan.EnableAPIs) > 0 {
		if err := c.enableAPIs(ctx, plan.EnableAPIs); err != nil {
			return err
		}
		say("enabled  %d API(s): %s", len(plan.EnableAPIs), strings.Join(short(plan.EnableAPIs), ", "))
	}
	if len(plan.GrantRoles) > 0 {
		if err := c.grantRoles(ctx, plan.Principal, plan.GrantRoles); err != nil {
			return err
		}
		say("granted  %d role(s) to %s", len(plan.GrantRoles), plan.Principal)
	}
	return nil
}

func (c *Client) enableAPIs(ctx context.Context, apis []string) error {
	url := c.url(serviceuseHost, "/v1/projects/%s/services:batchEnable", c.Project)
	body := map[string]any{"serviceIds": apis}
	if err := c.do(ctx, http.MethodPost, url, body, nil); err != nil {
		return Classify(err, c.Project, "enable APIs", "roles/serviceusage.serviceUsageAdmin")
	}
	return nil
}

// grantRoles reads the policy, adds the principal to each role, and
// writes it back.
//
// Read-modify-write on a document written whole, which is why the
// client never retries :setIamPolicy — a retry can clobber a change
// somebody else made in between. The etag is the guard: a concurrent
// write makes this one fail rather than silently win.
func (c *Client) grantRoles(ctx context.Context, principal string, roles []string) error {
	policy, err := c.projectPolicy(ctx)
	if err != nil {
		return err
	}
	for _, role := range roles {
		found := false
		for i, b := range policy.Bindings {
			if b.Role != role {
				continue
			}
			found = true
			policy.Bindings[i].Members = appendMissing(b.Members, principal)
		}
		if !found {
			policy.Bindings = append(policy.Bindings, struct {
				Role    string   `json:"role"`
				Members []string `json:"members"`
			}{Role: role, Members: []string{principal}})
		}
	}
	url := c.url(crmHost, "/v1/projects/%s:setIamPolicy", c.Project)
	if err := c.do(ctx, http.MethodPost, url, map[string]any{"policy": policy}, nil); err != nil {
		return Classify(err, c.Project, "change the project's IAM policy", "roles/resourcemanager.projectIamAdmin")
	}
	return nil
}

func appendMissing(members []string, want string) []string {
	for _, m := range members {
		if m == want {
			return members
		}
	}
	return append(members, want)
}

// BillingEnabled reports whether the project can be billed, and whether
// the answer is known at all.
//
// Unknown is not a failure: reading billing needs its own permission,
// and a setup that refused to run because it could not CHECK would be
// worse than one that lets the first real API call say so. But when it
// is known and false, everything after this fails with messages about
// APIs and quotas, so it is worth its own sentence.
func (c *Client) BillingEnabled(ctx context.Context) (enabled, known bool) {
	var info struct {
		BillingEnabled bool `json:"billingEnabled"`
	}
	url := c.url(billingHost, "/v1/projects/%s/billingInfo", c.Project)
	if err := c.do(ctx, http.MethodGet, url, nil, &info); err != nil {
		return false, false
	}
	return info.BillingEnabled, true
}

// CreateDatastore creates the project's datastore-mode database.
//
// Its own command, and its own function, because of one property: the
// location cannot be changed afterward. Everything else tako provisions
// is derivable, replaceable or revocable; this is the one call whose
// consequence outlives every later decision, so the region is typed by
// a human rather than defaulted (SC2 D4, owner 2026-08-10).
func (c *Client) CreateDatastore(ctx context.Context, location string) error {
	url := c.url(firestoreHost, "/v1/projects/%s/databases?databaseId=(default)", c.Project)
	body := map[string]any{"type": "DATASTORE_MODE", "locationId": location}
	if err := c.do(ctx, http.MethodPost, url, body, nil); err != nil {
		if ae, ok := asAPIError(err); ok && ae.Status == http.StatusConflict {
			return fmt.Errorf("%s already has a database — its location cannot be changed, so there is nothing to do here", c.Project)
		}
		return Classify(err, c.Project, "create the Datastore database", "roles/datastore.owner")
	}
	return nil
}

// DatastoreExists reports whether the project already has one.
func (c *Client) DatastoreExists(ctx context.Context) bool {
	var out struct {
		Name string `json:"name"`
	}
	url := c.url(firestoreHost, "/v1/projects/%s/databases/(default)", c.Project)
	return c.do(ctx, http.MethodGet, url, nil, &out) == nil
}

// short trims the .googleapis.com suffix for display.
func short(apis []string) []string {
	out := make([]string, len(apis))
	for i, a := range apis {
		out[i] = strings.TrimSuffix(a, ".googleapis.com")
	}
	sort.Strings(out)
	return out
}

// WaitForAPIs re-reads each service until it reports ENABLED. batchEnable
// returns before the change is visible everywhere, and a grant issued
// into that window fails with a message about the API rather than the
// timing — the same propagation lag the deploy already tolerates for
// IAM and Secret Manager.
func (c *Client) WaitForAPIs(ctx context.Context, apis []string, within time.Duration) {
	deadline := time.Now().Add(within)
	for _, api := range apis {
		for time.Now().Before(deadline) {
			var state struct {
				State string `json:"state"`
			}
			url := c.url(serviceuseHost, "/v1/projects/%s/services/%s", c.Project, api)
			if err := c.do(ctx, http.MethodGet, url, nil, &state); err != nil || state.State == "ENABLED" {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
}
