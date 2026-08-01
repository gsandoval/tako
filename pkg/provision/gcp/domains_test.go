package gcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/provision"
)

// The drift trap every applier can fall into: LiveHash is computed from
// the PARSED response, SpecHash from the desired map, and if the two
// shapes disagree by even a key name, every deploy reports drift and
// overwrites a correct object forever. Round-trip the create body through
// the response type and require the hashes to match.
func TestDomainMappingLiveHashMatchesSpecHash(t *testing.T) {
	r := &DomainMappings{
		C:      &Client{Project: "p", Region: "us-central1"},
		Labels: map[string]string{"tako-app": "blog", "tako-env": "prod"},
	}
	obj := provision.Object{
		Kind: provision.KindDomainMapping,
		Name: "blog.example.com",
		Spec: map[string]any{"routeName": "blog", "certificateMode": "AUTOMATIC"},
	}

	raw, err := json.Marshal(r.body(obj))
	if err != nil {
		t.Fatal(err)
	}
	var live domainMapping
	if err := json.Unmarshal(raw, &live); err != nil {
		t.Fatal(err)
	}

	liveHash := provision.SpecHash(map[string]any{
		"routeName":       live.Spec.RouteName,
		"certificateMode": live.Spec.CertificateMode,
	})
	if want := provision.SpecHash(obj.Spec); liveHash != want {
		t.Errorf("LiveHash %s != SpecHash %s — a fresh mapping would report drift on every deploy", liveHash, want)
	}
	if got := live.Metadata.Annotations["tako-spec-hash"]; got != provision.SpecHash(obj.Spec) {
		t.Errorf("recorded spec-hash annotation = %q, want %q", got, provision.SpecHash(obj.Spec))
	}
	if live.Metadata.Name != "blog.example.com" {
		t.Errorf("metadata.name = %q, want the domain", live.Metadata.Name)
	}
	// Labels are the managed marker: List filters on them, so a mapping
	// created without them would be invisible to its own destroy.
	if !hasLabels(live.Metadata.Labels, r.Labels) {
		t.Errorf("metadata.labels = %v, want the tako marker labels", live.Metadata.Labels)
	}
}

// tako never sets forceOverride — taking a domain from whatever holds it
// is the silent destruction §4.6a forbids.
func TestDomainMappingNeverForcesOverride(t *testing.T) {
	r := &DomainMappings{C: &Client{Project: "p", Region: "us-central1"}}
	raw, _ := json.Marshal(r.body(provision.Object{
		Name: "blog.example.com",
		Spec: map[string]any{"routeName": "blog", "certificateMode": "AUTOMATIC"},
	}))
	if strings.Contains(string(raw), "forceOverride") {
		t.Errorf("create body carries forceOverride: %s", raw)
	}
}

// The three failures a domain mapping has that the generic taxonomy
// cannot name: already mapped, not verified, and no such service. Each
// is a different problem with a different fix, so each gets its own text.
func TestDomainMappingClassify(t *testing.T) {
	r := &DomainMappings{C: &Client{Project: "tako-test-02", Region: "us-central1"}}

	conflict := r.classify(&apiError{Status: http.StatusConflict, Message: "already exists"}, "blog.example.com", "blog", "create")
	for _, want := range []string{"already mapped", "will not take it", "gcloud beta run domain-mappings delete"} {
		if !strings.Contains(conflict.Error(), want) {
			t.Errorf("409 text missing %q: %s", want, conflict)
		}
	}

	unverified := r.classify(&apiError{Status: http.StatusBadRequest, Message: "Domain is not verified for the project"}, "blog.example.com", "blog", "create")
	for _, want := range []string{"not verified", "gcloud domains verify example.com", "Search Console"} {
		if !strings.Contains(unverified.Error(), want) {
			t.Errorf("verification text missing %q: %s", want, unverified)
		}
	}
	// An apex domain verifies as itself, not as its (nonexistent) parent.
	apex := r.classify(&apiError{Status: http.StatusBadRequest, Message: "domain not verified"}, "example.com", "blog", "create")
	if !strings.Contains(apex.Error(), "gcloud domains verify example.com") {
		t.Errorf("apex verification text should verify the apex itself: %s", apex)
	}

	missing := r.classify(&apiError{Status: http.StatusNotFound, Message: "route not found"}, "blog.example.com", "blog", "create")
	if !strings.Contains(missing.Error(), "no run service named blog") {
		t.Errorf("404 text should name the missing route: %s", missing)
	}

	// A disabled API still routes to the standard enable-it remediation.
	disabled := r.classify(&apiError{Status: http.StatusForbidden, Reason: "SERVICE_DISABLED",
		Message: "run.googleapis.com API has not been used"}, "blog.example.com", "blog", "create")
	if !strings.Contains(disabled.Error(), "gcloud services enable run.googleapis.com") {
		t.Errorf("disabled-API text should keep the standard remediation: %s", disabled)
	}
}

func TestMappableRegions(t *testing.T) {
	if !RegionMaps("us-central1") {
		t.Error("us-central1 (tako's default region) must support domain mappings")
	}
	if RegionMaps("europe-west2") {
		t.Error("europe-west2 does not support domain mappings")
	}
}
