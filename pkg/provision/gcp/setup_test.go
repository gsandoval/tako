package gcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setupServer is a fake project: which APIs are on, which permissions
// the caller holds, and a record of every mutating call.
type setupServer struct {
	enabled map[string]bool
	perms   []string
	policy  map[string][]string // role → members
	writes  []string            // method+path of everything non-GET
	srv     *httptest.Server
}

func newSetupServer(t *testing.T, enabled map[string]bool, perms []string) *setupServer {
	t.Helper()
	f := &setupServer{enabled: enabled, perms: perms, policy: map[string][]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method != http.MethodGet {
			f.writes = append(f.writes, r.Method+" "+path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(path, "/billingInfo"):
			_, _ = w.Write([]byte(`{"billingEnabled":true}`))
		case strings.Contains(path, ":testIamPermissions"):
			out, _ := json.Marshal(map[string]any{"permissions": f.perms})
			_, _ = w.Write(out)
		case strings.Contains(path, ":getIamPolicy"):
			var bindings []map[string]any
			for role, members := range f.policy {
				bindings = append(bindings, map[string]any{"role": role, "members": members})
			}
			out, _ := json.Marshal(map[string]any{"bindings": bindings, "etag": "abc"})
			_, _ = w.Write(out)
		case strings.Contains(path, ":setIamPolicy"):
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				Policy struct {
					Bindings []struct {
						Role    string   `json:"role"`
						Members []string `json:"members"`
					} `json:"bindings"`
				} `json:"policy"`
			}
			_ = json.Unmarshal(raw, &body)
			for _, b := range body.Policy.Bindings {
				f.policy[b.Role] = b.Members
			}
			_, _ = w.Write([]byte(`{}`))
		case strings.Contains(path, ":batchEnable"):
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				ServiceIDs []string `json:"serviceIds"`
			}
			_ = json.Unmarshal(raw, &body)
			for _, id := range body.ServiceIDs {
				f.enabled[id] = true
			}
			_, _ = w.Write([]byte(`{"done":true}`))
		case strings.Contains(path, "/services/"):
			api := path[strings.LastIndex(path, "/")+1:]
			state := "DISABLED"
			if f.enabled[api] {
				state = "ENABLED"
			}
			_, _ = w.Write([]byte(`{"state":"` + state + `"}`))
		case strings.Contains(path, "/databases"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no database"}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *setupServer) client() *Client {
	return &Client{HC: f.srv.Client(), Project: "p", Region: "us-central1", Base: f.srv.URL}
}

// A project with nothing on and nothing granted: the plan is everything
// the graph needs, and the ROLES are only the ones this graph demands —
// the owner's ruling (2026-08-10) that setup grants the tighter set.
func TestThePlanIsWhatThisGraphNeedsAndNoMore(t *testing.T) {
	f := newSetupServer(t, map[string]bool{}, nil)
	plain, err := f.client().PlanSetup(context.Background(), Needs{}, "user:a@b.c", true)
	if err != nil {
		t.Fatal(err)
	}
	withAsync, err := f.client().PlanSetup(context.Background(), Needs{PubSub: true, Scheduler: true}, "user:a@b.c", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(withAsync.EnableAPIs) <= len(plain.EnableAPIs) {
		t.Errorf("an app with queues and crons needs MORE APIs than one without: %v vs %v",
			plain.EnableAPIs, withAsync.EnableAPIs)
	}
	if !has(withAsync.GrantRoles, "roles/pubsub.admin") {
		t.Error("an app with queues must be granted pubsub.admin")
	}
	// The point of the tighter set: an app with no queues never sees it.
	if has(plain.GrantRoles, "roles/pubsub.admin") {
		t.Error("an app with no queues was granted pubsub.admin — setup grants what the " +
			"graph needs, not the full bundle")
	}
	if has(plain.GrantRoles, "roles/cloudscheduler.admin") {
		t.Error("an app with no crons was granted cloudscheduler.admin")
	}
}

// Planning reads. A --plan run that mutated would be the worst kind of
// surprise, so the fake fails the test rather than the assertion.
func TestPlanningMutatesNothing(t *testing.T) {
	f := newSetupServer(t, map[string]bool{}, nil)
	if _, err := f.client().PlanSetup(context.Background(), Needs{Datastore: true}, "user:a@b.c", true); err != nil {
		t.Fatal(err)
	}
	for _, w := range f.writes {
		// testIamPermissions and getIamPolicy are POSTs that read.
		if strings.Contains(w, ":testIamPermissions") || strings.Contains(w, ":getIamPolicy") {
			continue
		}
		t.Errorf("planning issued a mutating call: %s", w)
	}
}

// Converging twice does nothing the second time. Setup is a command you
// re-run as the app grows (the tighter-set ruling makes that the normal
// case), so idempotence is the property, not a nicety.
func TestASecondRunHasNothingToDo(t *testing.T) {
	f := newSetupServer(t, map[string]bool{}, nil)
	ctx := context.Background()
	needs := Needs{PubSub: true}

	first, err := f.client().PlanSetup(ctx, needs, "user:a@b.c", false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Empty() {
		t.Fatal("a bare project should have work to do")
	}
	if err := f.client().ApplySetup(ctx, first, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}

	second, err := f.client().PlanSetup(ctx, needs, "user:a@b.c", false)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Empty() {
		t.Errorf("a converged project still wants %v / %v", second.EnableAPIs, second.GrantRoles)
	}
	if second.HeldAPIs != first.HeldAPIs+len(first.EnableAPIs) {
		t.Errorf("held APIs = %d, want %d", second.HeldAPIs, first.HeldAPIs+len(first.EnableAPIs))
	}
}

// Granting to a CI service account cannot use testIamPermissions —
// that answers for the CALLER. Getting this wrong would report every
// role as already held (because the human running setup holds them) and
// grant the service account nothing at all, silently.
func TestGrantingToAnotherPrincipalReadsThePolicyNotTheCaller(t *testing.T) {
	// The caller holds everything; the service account holds nothing.
	f := newSetupServer(t, map[string]bool{}, []string{
		"run.services.create", "run.services.update", "run.services.setIamPolicy",
		"iam.serviceAccounts.create", "iam.serviceAccounts.actAs",
		"artifactregistry.repositories.create", "storage.buckets.create",
		"resourcemanager.projects.setIamPolicy", "iam.serviceAccounts.setIamPolicy",
	})
	plan, err := f.client().PlanSetup(context.Background(), Needs{},
		"serviceAccount:ci@p.iam.gserviceaccount.com", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.GrantRoles) == 0 {
		t.Fatal("the service account holds nothing and was granted nothing — this is the " +
			"failure that leaves CI unable to deploy while setup reports success")
	}
}

// Applying adds the principal to each role and leaves everyone else's
// bindings alone: the policy is a document written whole, so a careless
// write removes other people.
func TestApplyingKeepsBindingsItDidNotComeFor(t *testing.T) {
	f := newSetupServer(t, map[string]bool{}, nil)
	f.policy["roles/viewer"] = []string{"user:someone@else.com"}
	ctx := context.Background()
	plan, err := f.client().PlanSetup(ctx, Needs{}, "user:a@b.c", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client().ApplySetup(ctx, plan, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if got := f.policy["roles/viewer"]; len(got) != 1 || got[0] != "user:someone@else.com" {
		t.Errorf("an unrelated binding was changed: %v", got)
	}
	if !has(f.policy["roles/run.admin"], "user:a@b.c") {
		t.Errorf("the principal was not added to run.admin: %v", f.policy["roles/run.admin"])
	}
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// A brand new project has serviceusage on and cloudresourcemanager OFF.
// Every IAM read goes through the latter, so setup — the command whose
// whole job is preparing a project — could not prepare one: it asked
// about permissions, got "the cloudresourcemanager API is disabled", and
// stopped. Found by running against a fresh project (2026-08-10).
func TestABrandNewProjectPlansTheAPIThatMakesIAMReadable(t *testing.T) {
	// Exactly what GCP gives you: serviceusage and datastore on, and
	// nothing else tako needs.
	f := newSetupServer(t, map[string]bool{"datastore.googleapis.com": true}, nil)
	plan, err := f.client().PlanSetup(context.Background(), Needs{}, "user:a@b.c", true)
	if err != nil {
		t.Fatalf("a fresh project must be plannable, not an error: %v", err)
	}
	if !has(plan.EnableAPIs, "cloudresourcemanager.googleapis.com") {
		t.Errorf("cloudresourcemanager is not in the plan (%v) — without it no IAM policy can "+
			"be read or written, so every derived runtime grant and the ProjectNumber "+
			"lookup fail", plan.EnableAPIs)
	}
	if !plan.PermissionsUnknown {
		t.Error("the roles were reported as known while the API every IAM read needs is off")
	}
	if plan.Empty() {
		t.Error("a plan that cannot check permissions is not an empty plan — there is an API " +
			"to turn on before anything can be known")
	}
}

// The first failure a project with no billing account produces, and it
// arrives as a 400 from serviceusage naming services the user never
// mentioned. Without its own text it reads like a tako bug.
func TestNoBillingAccountSaysSo(t *testing.T) {
	err := Classify(&apiError{
		Status: 400,
		API:    "serviceusage.googleapis.com",
		Message: "Billing account for project '498246572877' is not found. Billing must be " +
			"enabled for activation of service(s) 'run.googleapis.com' to proceed.",
	}, "tako-test-04", "enable APIs", "roles/serviceusage.serviceUsageAdmin")
	for _, want := range []string{
		"tako-test-04 has no billing account",
		"https://console.cloud.google.com/billing/linkedaccount?project=tako-test-04",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("billing refusal missing %q:\n%s", want, err)
		}
	}
}
