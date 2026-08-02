package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/provision"
)

// DomainMappings manages Cloud Run domain mappings (U1): the custom
// domain in front of a service, with a Google-managed certificate.
//
// The mapping is a Knative-style resource, so metadata carries real
// LABELS — the same managed marker every other kind uses, no description
// marker (scheduler.go) or name suffix (tasks.go) needed — and
// annotations carry the spec hash.
//
// There is no update verb: spec.routeName is immutable, so re-pointing a
// domain at another service is a delete + create, classified as a
// REPLACE so the plan states the consequence first (the domain stops
// answering until the new mapping's certificate is ready).
//
// tako never sets spec.forceOverride: stealing a domain another service
// in the project already answers on is exactly the silent destruction
// §4.6a forbids. The 409 says so instead.
type DomainMappings struct {
	C      *Client
	Labels map[string]string
	Say    func(string, ...any)
}

func (r *DomainMappings) Kind() provision.Kind { return provision.KindDomainMapping }

// base is the REGIONAL endpoint: domain mappings live in the region
// their service does, so the call must not go to the global host.
func (r *DomainMappings) base() string {
	// The regional host is the host, so it goes through url like every
	// other: a test redirecting Base catches this call too.
	return r.C.url(r.C.Region+"-"+runHost,
		"/apis/domains.cloudrun.com/v1/namespaces/%s/domainmappings", r.C.Project)
}

type domainMapping struct {
	Metadata struct {
		Name        string            `json:"name,omitempty"`
		Namespace   string            `json:"namespace,omitempty"`
		Labels      map[string]string `json:"labels,omitempty"`
		Annotations map[string]string `json:"annotations,omitempty"`
	} `json:"metadata"`
	Spec struct {
		RouteName       string `json:"routeName,omitempty"`
		CertificateMode string `json:"certificateMode,omitempty"`
	} `json:"spec"`
	Status struct {
		Conditions []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
		ResourceRecords []struct {
			Name   string `json:"name"`
			Rrdata string `json:"rrdata"`
			Type   string `json:"type"`
		} `json:"resourceRecords"`
		MappedRouteName string `json:"mappedRouteName"`
		URL             string `json:"url"`
	} `json:"status"`
}

func (r *DomainMappings) List(ctx context.Context) ([]provision.Observed, error) {
	var out struct {
		Items []domainMapping `json:"items"`
	}
	if err := r.C.do(ctx, http.MethodGet, r.base(), nil, &out); err != nil {
		if disabledAPI(err) {
			return nil, nil // API off ⇒ nothing of ours can exist
		}
		return nil, Classify(err, r.C.Project, "list domain mappings", "roles/run.viewer")
	}
	var observed []provision.Observed
	for _, m := range out.Items {
		if !hasLabels(m.Metadata.Labels, r.Labels) {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind:     provision.KindDomainMapping,
			Name:     m.Metadata.Name,
			SpecHash: m.Metadata.Annotations["tako-spec-hash"],
			LiveHash: provision.SpecHash(map[string]any{
				"routeName":       m.Spec.RouteName,
				"certificateMode": m.Spec.CertificateMode,
			}),
			// The live route is the immutable field change
			// classification keys on.
			Detail: map[string]string{"routeName": m.Spec.RouteName},
		})
	}
	return observed, nil
}

func (r *DomainMappings) body(o provision.Object) map[string]any {
	meta := map[string]any{
		"name":        o.Name,
		"namespace":   r.C.Project,
		"labels":      r.Labels,
		"annotations": map[string]string{"tako-spec-hash": provision.SpecHash(o.Spec)},
	}
	return map[string]any{
		"apiVersion": "domains.cloudrun.com/v1",
		"kind":       "DomainMapping",
		"metadata":   meta,
		"spec": map[string]any{
			"routeName":       o.Spec["routeName"],
			"certificateMode": o.Spec["certificateMode"],
		},
	}
}

func (r *DomainMappings) Create(ctx context.Context, o provision.Object) error {
	if err := r.C.do(ctx, http.MethodPost, r.base(), r.body(o), nil); err != nil {
		return r.classify(err, o.Name, fmt.Sprint(o.Spec["routeName"]), "create")
	}
	return nil
}

// Update is delete + create: the API has no update verb and routeName is
// immutable. The plan classified this as a REPLACE, so the consequence
// was stated before anything happened; this narrates it again as it runs.
//
// The delete is ASYNCHRONOUS. It returns as soon as the mapping is
// marked for deletion, and the resource keeps answering GET for a few
// seconds after — so creating immediately raced our own teardown and
// came back "already mapped", blaming the user for holding a domain
// tako was in the middle of releasing. Wait for it to actually read back
// gone, the same discipline grant revocation already follows.
func (r *DomainMappings) Update(ctx context.Context, o provision.Object) error {
	if r.Say != nil {
		r.Say("⚠ replacing domain mapping %s (route → %s): delete + create; the domain stops answering until the new certificate is ready",
			o.Name, o.Spec["routeName"])
	}
	if err := r.Delete(ctx, o.Name); err != nil {
		return err
	}
	if err := r.waitGone(ctx, o.Name); err != nil {
		return err
	}
	return r.Create(ctx, o)
}

// mappingGoneWait bounds how long a delete may take to become visible.
const mappingGoneWait = 2 * time.Minute

// waitGone blocks until the mapping truly reads back absent. Converging
// on the OBSERVED state rather than on the delete call's return is what
// makes the replace safe to retry.
func (r *DomainMappings) waitGone(ctx context.Context, domain string) error {
	deadline := time.Now().Add(mappingGoneWait)
	for {
		err := r.C.do(ctx, http.MethodGet, r.base()+"/"+domain, nil, nil)
		if notFound(err) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("domain mapping %s did not finish deleting within %s — re-run tako deploy; the replace resumes from wherever it got to",
				domain, mappingGoneWait)
		}
		time.Sleep(2 * time.Second)
	}
}

func (r *DomainMappings) Delete(ctx context.Context, name string) error {
	if err := r.C.do(ctx, http.MethodDelete, r.base()+"/"+name, nil, nil); err != nil {
		if notFound(err) {
			return nil // already gone: destroy converges
		}
		return r.classify(err, name, "", "delete")
	}
	return nil
}

// Status reports how far along the domain is: the DNS records still to
// create (empty once they resolve), whether the certificate is live, and
// the served URL. Deploys are stateless and resumable, so every run
// re-reads this and narrates where the domain actually is.
type Status struct {
	Records   []Record
	Ready     bool
	Reason    string
	Message   string
	URL       string
	RouteName string
}

// Record is one DNS record the user must create at their registrar.
type Record struct{ Name, Type, Data string }

func (r *DomainMappings) Status(ctx context.Context, domain string) (*Status, error) {
	var m domainMapping
	if err := r.C.do(ctx, http.MethodGet, r.base()+"/"+domain, nil, &m); err != nil {
		return nil, r.classify(err, domain, "", "read")
	}
	st := &Status{URL: m.Status.URL, RouteName: m.Spec.RouteName}
	for _, rec := range m.Status.ResourceRecords {
		name := rec.Name
		if name == "" {
			// An apex domain's records carry no label; "@" is what every
			// registrar's host field calls the zone root.
			name = "@"
		}
		st.Records = append(st.Records, Record{Name: name, Type: rec.Type, Data: rec.Rrdata})
	}
	for _, c := range m.Status.Conditions {
		if c.Type != "Ready" {
			continue
		}
		st.Ready = c.Status == "True"
		st.Reason, st.Message = c.Reason, c.Message
	}
	return st, nil
}

// classify adds the failures that are specific to domain mappings to the
// standard taxonomy: an unverified domain and a domain already spoken
// for are different problems with different fixes, and neither is a
// permission gap the generic 403 text would name correctly.
func (r *DomainMappings) classify(err error, domain, route, verb string) error {
	ae, ok := asAPIError(err)
	if !ok {
		return err
	}
	msg := strings.ToLower(ae.Message)
	switch {
	case ae.Status == http.StatusConflict,
		ae.Status == http.StatusBadRequest && strings.Contains(msg, "already exists"):
		return fmt.Errorf("%s is already mapped in %s — a domain answers for one service, and tako will not take it from whatever holds it; remove that mapping (gcloud beta run domain-mappings delete --domain %s --region %s) or choose another name",
			domain, r.C.Project, domain, r.C.Region)
	case strings.Contains(msg, "verif"):
		base := domain
		if i := strings.Index(domain, "."); i >= 0 && strings.Count(domain, ".") > 1 {
			base = domain[i+1:]
		}
		return fmt.Errorf("%s is not verified for %s — prove you own it once: gcloud domains verify %s (this opens Search Console), then re-run tako deploy",
			domain, r.C.Project, base)
	case ae.Status == http.StatusNotFound && route != "":
		return fmt.Errorf("cannot map %s: no run service named %s in %s/%s — deploy the service before its domain",
			domain, route, r.C.Project, r.C.Region)
	}
	return Classify(err, r.C.Project, verb+" the domain mapping", "roles/run.admin")
}

// MappableRegions are the regions Cloud Run domain mappings exist in.
// The list is short and load-bearing: deploying a domain outside it fails
// at the API with a shape no taxonomy entry reads well, so preflight
// refuses first (docs/domain-options.md records why this backend, and
// what the alternatives cost).
var MappableRegions = []string{
	"asia-east1", "asia-northeast1", "asia-southeast1",
	"europe-north1", "europe-west1", "europe-west4",
	"us-central1", "us-east1", "us-east4", "us-west1",
}

// RegionMaps reports whether region supports domain mappings.
func RegionMaps(region string) bool {
	for _, r := range MappableRegions {
		if r == region {
			return true
		}
	}
	return false
}

// verifiedDomains lists the domains this project has proven ownership of
// (Search Console). tako never performs the verification — it is a
// genuinely interactive consent flow, like the OAuth screen — so the
// only honest move is to check and print the command.
func (c *Client) verifiedDomains(ctx context.Context) ([]string, error) {
	var out struct {
		Domains []struct {
			ID string `json:"id"`
		} `json:"domains"`
	}
	url := c.url(runHost, "/v1/projects/"+c.Project+"/authorizeddomains")
	if err := c.do(ctx, http.MethodGet, url, nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Domains))
	for _, d := range out.Domains {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

// verifiedFor reports whether domain is covered by a verified entry:
// verifying example.com covers blog.example.com, the way Search Console
// treats subdomains.
func verifiedFor(verified []string, domain string) bool {
	for _, v := range verified {
		if domain == v || strings.HasSuffix(domain, "."+v) {
			return true
		}
	}
	return false
}

// verifiableBase is the name the user actually verifies: the registrable
// parent for a subdomain, the domain itself for an apex.
func verifiableBase(domain string) string { return ZoneOf(domain) }

// ZoneOf is the DNS zone a domain's records live in — the parent for a
// subdomain, the domain itself for an apex. Cloud Run returns record
// names relative to it, so the UI names the zone rather than rewriting
// the records into FQDNs a registrar would then double up.
func ZoneOf(domain string) string {
	if strings.Count(domain, ".") > 1 {
		return domain[strings.Index(domain, ".")+1:]
	}
	return domain
}
