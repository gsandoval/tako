package gcp

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gsandoval/tako/pkg/provision"
)

// Grants reconciles the tako-managed slice of IAM policies
// (KindIamGrants). Policies carry no labels, so observation is
// content-addressed against the desired targets: List reads each
// desired target's policy, extracts the bindings whose members are
// THIS app+env's identities (ManagedSA — the marker-derived email set,
// plus the service agents on tako-managed target kinds), and hashes
// them the same way Desired hashes its spec — equal means converged.
// Apply is read-modify-write: strip managed bindings, add the desired
// set, write back with the etag. Foreign bindings — allUsers, humans,
// other tools, and crucially OTHER ENVS' service accounts on the
// shared project policy — are never touched.
type Grants struct {
	C       *Client
	Desired []provision.Object
	// ManagedSA is the exact email set of this app+env's SAs
	// (ServiceAccounts.ManagedEmails). A name-pattern predicate here
	// once stripped prod's grants during a preview env's destroy.
	ManagedSA map[string]bool
}

func (g *Grants) Kind() provision.Kind { return provision.KindIamGrants }

type policy struct {
	Bindings []struct {
		Role    string   `json:"role"`
		Members []string `json:"members"`
	} `json:"bindings"`
	Etag string `json:"etag,omitempty"`
}

// target extracts the typed target from a grants object spec.
func grantTarget(o provision.Object) (typ, id string) {
	t, _ := o.Spec["target"].(map[string]any)
	return fmt.Sprint(t["type"]), fmt.Sprint(t["id"])
}

// urls returns the getIamPolicy and setIamPolicy calls for a target.
// set bodies wrap the policy except for GCS, which PUTs it bare.
func (g *Grants) urls(typ, id string) (getMethod, getURL, setURL string, bareSet bool, err error) {
	switch typ {
	case provision.TargetProject:
		base := "https://cloudresourcemanager.googleapis.com/v1/projects/" + g.C.Project
		return http.MethodPost, base + ":getIamPolicy", base + ":setIamPolicy", false, nil
	case provision.TargetBucket:
		base := "https://storage.googleapis.com/storage/v1/b/" + id + "/iam"
		return http.MethodGet, base, base, true, nil
	case provision.TargetTopic:
		base := "https://pubsub.googleapis.com/v1/projects/" + g.C.Project + "/topics/" + id
		return http.MethodGet, base + ":getIamPolicy", base + ":setIamPolicy", false, nil
	case provision.TargetRunService:
		base := fmt.Sprintf("https://run.googleapis.com/v2/projects/%s/locations/%s/services/%s", g.C.Project, g.C.Region, id)
		return http.MethodGet, base + ":getIamPolicy", base + ":setIamPolicy", false, nil
	case provision.TargetServiceAccount:
		base := "https://iam.googleapis.com/v1/projects/" + g.C.Project + "/serviceAccounts/" + id
		return http.MethodPost, base + ":getIamPolicy", base + ":setIamPolicy", false, nil
	case provision.TargetSubscription:
		base := "https://pubsub.googleapis.com/v1/projects/" + g.C.Project + "/subscriptions/" + id
		return http.MethodGet, base + ":getIamPolicy", base + ":setIamPolicy", false, nil
	case provision.TargetSecret:
		base := "https://secretmanager.googleapis.com/v1/projects/" + g.C.Project + "/secrets/" + id
		return http.MethodGet, base + ":getIamPolicy", base + ":setIamPolicy", false, nil
	}
	return "", "", "", false, fmt.Errorf("unknown grant target type %q", typ)
}

// managed reports whether a binding member on this target is tako's to
// rewrite. The Pub/Sub service agent counts on SA targets (token
// creator for push OIDC), subscription targets (dead-letter acking),
// and topic targets (dead-letter forwarding); the GCS service agent
// counts on topic targets (bucket-notification publishing) — the
// target kinds tako itself manages; everywhere else it is someone
// else's.
func (g *Grants) managed(typ, member string) bool {
	if email, ok := strings.CutPrefix(member, "serviceAccount:"); ok && g.ManagedSA[email] {
		return true
	}
	if !strings.HasPrefix(member, "serviceAccount:service-") {
		return false
	}
	pubsubAgent := strings.HasSuffix(member, "@gcp-sa-pubsub.iam.gserviceaccount.com")
	gcsAgent := strings.HasSuffix(member, "@gs-project-accounts.iam.gserviceaccount.com")
	switch typ {
	case provision.TargetServiceAccount, provision.TargetSubscription:
		return pubsubAgent
	case provision.TargetTopic:
		return pubsubAgent || gcsAgent
	}
	return false
}

// observedSpec rebuilds a grants object spec from a live policy — the
// content-addressed observation.
func (g *Grants) observedSpec(typ, id string, p policy) map[string]any {
	var pairs []string
	for _, b := range p.Bindings {
		for _, m := range b.Members {
			if g.managed(typ, m) {
				pairs = append(pairs, b.Role+"|"+m)
			}
		}
	}
	sort.Strings(pairs)
	bs := make([]any, 0, len(pairs))
	for _, pair := range pairs {
		role, member, _ := strings.Cut(pair, "|")
		bs = append(bs, map[string]any{"role": role, "member": member})
	}
	return map[string]any{
		"target":   map[string]any{"type": typ, "id": id},
		"bindings": bs,
	}
}

func (g *Grants) read(ctx context.Context, typ, id string) (policy, bool, error) {
	getMethod, getURL, _, _, err := g.urls(typ, id)
	if err != nil {
		return policy{}, false, err
	}
	var in any
	if getMethod == http.MethodPost {
		in = map[string]any{}
	}
	var p policy
	if err := g.C.do(ctx, getMethod, getURL, in, &p); err != nil {
		ae, isAPI := err.(*apiError)
		if isAPI && ae.Status == http.StatusNotFound {
			return policy{}, false, nil // target not created yet: no policy to observe
		}
		// Resource-scoped targets (SAs, run services, subscriptions…)
		// answer 403/400 rather than 404 once deleted — a partial
		// destroy must CONVERGE past them, not wedge forever. The
		// project target stays strict: a 403 there is a real
		// permission problem preflight should have caught.
		if isAPI && typ != provision.TargetProject &&
			(ae.Status == http.StatusForbidden || ae.Status == http.StatusBadRequest) {
			return policy{}, false, nil
		}
		return policy{}, false, Classify(err, g.C.Project, "read the IAM policy of "+typ+" "+id, "roles/viewer")
	}
	return p, true, nil
}

func (g *Grants) List(ctx context.Context) ([]provision.Observed, error) {
	var observed []provision.Observed
	for _, o := range g.Desired {
		if o.Kind != provision.KindIamGrants {
			continue
		}
		typ, id := grantTarget(o)
		p, ok, err := g.read(ctx, typ, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind:     provision.KindIamGrants,
			Name:     o.Name,
			SpecHash: provision.SpecHash(g.observedSpec(typ, id, p)),
		})
	}
	return observed, nil
}

func (g *Grants) apply(ctx context.Context, o provision.Object) error {
	typ, id := grantTarget(o)
	_, _, setURL, bareSet, err := g.urls(typ, id)
	if err != nil {
		return err
	}
	p, ok, err := g.read(ctx, typ, id)
	if err != nil {
		return err
	}
	if !ok {
		// The target does not exist (yet, or anymore): nothing holds
		// bindings to rewrite. Deploy sequences subscription-target
		// grants after their subscriptions exist; destroy hits this
		// after the target's own deletion — both converge.
		return nil
	}
	// Strip every managed member, then re-add the desired set.
	next := map[string][]string{} // role → members
	for _, b := range p.Bindings {
		for _, m := range b.Members {
			if !g.managed(typ, m) {
				next[b.Role] = append(next[b.Role], m)
			}
		}
	}
	if bs, ok := o.Spec["bindings"].([]any); ok {
		for _, raw := range bs {
			m, _ := raw.(map[string]any)
			role, member := fmt.Sprint(m["role"]), fmt.Sprint(m["member"])
			next[role] = append(next[role], member)
		}
	}
	roles := make([]string, 0, len(next))
	for role := range next {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	bindings := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		members := next[role]
		sort.Strings(members)
		bindings = append(bindings, map[string]any{"role": role, "members": members})
	}
	newPolicy := map[string]any{"bindings": bindings, "etag": p.Etag}
	var body any = map[string]any{"policy": newPolicy}
	method := http.MethodPost
	if bareSet {
		body, method = newPolicy, http.MethodPut
	}
	if err := g.C.do(ctx, method, setURL, body, nil); err != nil {
		// The target can die between read and write — or, on partial
		// destroys, before either (deleted resources' policy reads can
		// answer 200 tombstones while writes 404). A gone non-project
		// target has nothing left to revoke: converge.
		if ae, ok := err.(*apiError); ok && typ != provision.TargetProject &&
			(ae.Status == http.StatusNotFound || ae.Status == http.StatusForbidden || ae.Status == http.StatusBadRequest) {
			return nil
		}
		return Classify(err, g.C.Project, "write the IAM policy of "+typ+" "+id, "roles/resourcemanager.projectIamAdmin")
	}
	return nil
}

func (g *Grants) Create(ctx context.Context, o provision.Object) error { return g.apply(ctx, o) }
func (g *Grants) Update(ctx context.Context, o provision.Object) error { return g.apply(ctx, o) }

// Delete strips every managed binding from the named target — destroy's
// revocation pass (grants are not data; §4.6a protects data only).
func (g *Grants) Delete(ctx context.Context, name string) error {
	for _, o := range g.Desired {
		if o.Kind == provision.KindIamGrants && o.Name == name {
			bare := provision.Object{Kind: o.Kind, Name: o.Name, Spec: map[string]any{
				"target":   o.Spec["target"],
				"bindings": []any{},
			}}
			return g.apply(ctx, bare)
		}
	}
	return nil
}
