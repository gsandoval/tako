package gcp

// CR2: the ingress stance is a two-way setting that was wired one way.
//
// allowPublic only ever ADDED the allUsers invoker binding, so declaring
// a deployed public route Private left it publicly invokable — and,
// because isPublic feeds liveHash, the plan reported that service as
// changed on every deploy from then on and never converged.
//
// The write was also a blind setIamPolicy carrying exactly one binding,
// which replaces the whole policy: every other binding on a public
// service — a teammate's run.viewer, another tool's grant — went with it,
// against the read-modify-write discipline grants.go states as its rule.

import (
	"context"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/provision"
)

func ingressSpec(public bool) map[string]any {
	return map[string]any{
		"image":          "gcr.io/p/app@sha256:abc",
		"serviceAccount": "app-sa@p.iam.gserviceaccount.com",
		"env":            map[string]any{"TAKO_BACKEND": "gcp"},
		"public":         public,
		"scaling": map[string]any{
			"min": 0, "max": 100, "concurrency": 80,
			"cpu": "1", "memory": "512Mi",
			"cpuAlwaysOn": false, "startupBoost": false,
			"affinity": false, "http2": true, "timeout": 300,
		},
	}
}

// livePolicy stages the policy the fake answers getIamPolicy with.
//
// One reply key, not two: the fake matches by PREFIX, and
// ".../services/app" is a prefix of ".../services/app:getIamPolicy", so
// two keys race on map iteration order. The body carries both the
// operation fields the PATCH needs and the policy fields the read needs,
// which makes the answer the same whichever call arrives.
func livePolicy(f *fakeAPI, bindings ...map[string]any) {
	members := make([]any, 0, len(bindings))
	for _, b := range bindings {
		members = append(members, b)
	}
	f.reply[runHost+"/v2/projects/p/locations/us-central1/services/app"] = map[string]any{
		"bindings": members,
		"etag":     "e1",
		"name":     "operations/1",
		"done":     true,
	}
}

func binding(role string, members ...string) map[string]any {
	m := make([]any, 0, len(members))
	for _, s := range members {
		m = append(m, s)
	}
	return map[string]any{"role": role, "members": m}
}

// setPolicySent returns the policy body of the last setIamPolicy, and
// whether one was sent at all.
func setPolicySent(f *fakeAPI) (map[string]any, bool) {
	for i := len(f.mu.calls) - 1; i >= 0; i-- {
		if strings.Contains(f.mu.calls[i].Path, ":setIamPolicy") {
			p, _ := f.mu.calls[i].Body["policy"].(map[string]any)
			return p, true
		}
	}
	return nil, false
}

func membersOf(policy map[string]any, role string) []string {
	var out []string
	bindings, _ := policy["bindings"].([]any)
	for _, raw := range bindings {
		b, _ := raw.(map[string]any)
		if b["role"] != role {
			continue
		}
		ms, _ := b["members"].([]any)
		for _, m := range ms {
			out = append(out, m.(string))
		}
	}
	return out
}

// The finding: this is the deploy that closes a door, and it did nothing.
func TestDeclaringAPublicServicePrivateRevokesAllUsers(t *testing.T) {
	f := newFakeAPI(t)
	livePolicy(f, binding("roles/run.invoker", "allUsers", "serviceAccount:caller@p.iam.gserviceaccount.com"))
	r := &RunServices{C: f.client()}

	if err := r.Update(context.Background(), provision.Object{
		Kind: provision.KindRunService, Name: "app", Spec: ingressSpec(false),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	policy, sent := setPolicySent(f)
	if !sent {
		t.Fatal("no policy write: the allUsers binding survives and the service stays open to the internet")
	}
	for _, m := range membersOf(policy, "roles/run.invoker") {
		if m == "allUsers" {
			t.Error("allUsers is still an invoker after the service was declared private")
		}
	}
}

// Closing the door must not take the other bindings with it: the
// caller service that legitimately invokes this one keeps its grant.
func TestRevokingPublicKeepsEveryOtherBinding(t *testing.T) {
	f := newFakeAPI(t)
	livePolicy(f,
		binding("roles/run.invoker", "allUsers", "serviceAccount:caller@p.iam.gserviceaccount.com"),
		binding("roles/run.viewer", "user:teammate@example.com"),
	)
	r := &RunServices{C: f.client()}

	if err := r.Update(context.Background(), provision.Object{
		Kind: provision.KindRunService, Name: "app", Spec: ingressSpec(false),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	policy, _ := setPolicySent(f)
	if got := membersOf(policy, "roles/run.invoker"); len(got) != 1 || got[0] != "serviceAccount:caller@p.iam.gserviceaccount.com" {
		t.Errorf("the calling service's invoker grant did not survive: %v", got)
	}
	if got := membersOf(policy, "roles/run.viewer"); len(got) != 1 || got[0] != "user:teammate@example.com" {
		t.Errorf("a foreign binding was destroyed: %v", got)
	}
}

// The other half of the same defect: making a service public used to
// REPLACE the policy, so everything else on it went away.
func TestGoingPublicPreservesForeignBindings(t *testing.T) {
	f := newFakeAPI(t)
	livePolicy(f,
		binding("roles/run.invoker", "serviceAccount:caller@p.iam.gserviceaccount.com"),
		binding("roles/run.viewer", "user:teammate@example.com"),
	)
	r := &RunServices{C: f.client()}

	if err := r.Update(context.Background(), provision.Object{
		Kind: provision.KindRunService, Name: "app", Spec: ingressSpec(true),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	policy, sent := setPolicySent(f)
	if !sent {
		t.Fatal("a public service was never granted allUsers")
	}
	invokers := membersOf(policy, "roles/run.invoker")
	var hasAll, hasCaller bool
	for _, m := range invokers {
		hasAll = hasAll || m == "allUsers"
		hasCaller = hasCaller || m == "serviceAccount:caller@p.iam.gserviceaccount.com"
	}
	if !hasAll {
		t.Errorf("allUsers was not granted: %v", invokers)
	}
	if !hasCaller {
		t.Errorf("the calling service's grant was wiped by going public: %v", invokers)
	}
	if got := membersOf(policy, "roles/run.viewer"); len(got) != 1 {
		t.Errorf("a foreign binding was destroyed by going public: %v", got)
	}
}

// Converged is converged: a public service that is already public must
// not be written every deploy. The etag makes the write a lost-update
// risk, and a no-op deploy that rewrites IAM is noise in the audit log.
func TestAnAlreadyCorrectPolicyIsNotRewritten(t *testing.T) {
	for _, public := range []bool{true, false} {
		f := newFakeAPI(t)
		if public {
			livePolicy(f, binding("roles/run.invoker", "allUsers"))
		} else {
			livePolicy(f, binding("roles/run.invoker", "serviceAccount:caller@p.iam.gserviceaccount.com"))
		}
		r := &RunServices{C: f.client()}
		if err := r.Update(context.Background(), provision.Object{
			Kind: provision.KindRunService, Name: "app", Spec: ingressSpec(public),
		}); err != nil {
			t.Fatalf("public=%v: update: %v", public, err)
		}
		if _, sent := setPolicySent(f); sent {
			t.Errorf("public=%v: the policy already said this; it was written anyway", public)
		}
	}
}

// The write carries the etag it read, so a concurrent change is
// refused by Google rather than silently overwritten.
func TestThePolicyWriteCarriesTheEtagItRead(t *testing.T) {
	f := newFakeAPI(t)
	livePolicy(f, binding("roles/run.invoker", "allUsers"))
	r := &RunServices{C: f.client()}
	if err := r.Update(context.Background(), provision.Object{
		Kind: provision.KindRunService, Name: "app", Spec: ingressSpec(false),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	policy, _ := setPolicySent(f)
	if policy["etag"] != "e1" {
		t.Errorf("etag not echoed: %v", policy["etag"])
	}
}
