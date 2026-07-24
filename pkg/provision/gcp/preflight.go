package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// preflightAPIs are the services every deploy touches.
var preflightAPIs = []string{
	"run.googleapis.com",
	"iam.googleapis.com",
	"artifactregistry.googleapis.com",
	"storage.googleapis.com",
}

// preflightPerms maps required permissions to the narrowest role that
// grants them — the remediation text names that role, never broader.
var preflightPerms = []struct{ perm, role string }{
	{"run.services.create", "roles/run.admin"},
	{"run.services.update", "roles/run.admin"},
	{"run.services.setIamPolicy", "roles/run.admin"},
	{"iam.serviceAccounts.create", "roles/iam.serviceAccountAdmin"},
	{"iam.serviceAccounts.actAs", "roles/iam.serviceAccountUser"},
	{"artifactregistry.repositories.create", "roles/artifactregistry.admin"},
	{"storage.buckets.create", "roles/storage.admin"},
	// Derived runtime grants are policy writes on the project and its
	// resources — every app has at least its services' policies.
	{"resourcemanager.projects.setIamPolicy", "roles/resourcemanager.projectIamAdmin"},
	{"iam.serviceAccounts.setIamPolicy", "roles/iam.serviceAccountAdmin"},
}

// Needs are the backend surfaces this app's graph actually uses; the
// preflight only probes what the deploy will touch.
type Needs struct {
	Datastore bool // any entity declared
	PubSub    bool // any queue or topic declared
}

// Preflight checks credentials, APIs, and permissions before any
// mutation — every gap is one line with its fix (preflight-as-UX).
// It returns the API short names it verified, for the CLI's ok line.
func (c *Client) Preflight(ctx context.Context, needs Needs) ([]string, error) {
	var gaps []string

	apis := append([]string{}, preflightAPIs...)
	perms := append([]struct{ perm, role string }{}, preflightPerms...)
	if needs.Datastore {
		apis = append(apis, "datastore.googleapis.com")
		perms = append(perms, struct{ perm, role string }{"datastore.indexes.create", "roles/datastore.indexAdmin"})
	}
	if needs.PubSub {
		apis = append(apis, "pubsub.googleapis.com")
		perms = append(perms,
			struct{ perm, role string }{"pubsub.topics.create", "roles/pubsub.admin"},
			struct{ perm, role string }{"pubsub.subscriptions.create", "roles/pubsub.admin"})
	}

	for _, api := range apis {
		var state struct {
			State string `json:"state"`
		}
		url := fmt.Sprintf("https://serviceusage.googleapis.com/v1/projects/%s/services/%s", c.Project, api)
		if err := c.do(ctx, http.MethodGet, url, nil, &state); err != nil {
			// Classify handles bad project / no auth; a serviceusage 403
			// means we cannot even ask — report and stop.
			return nil, Classify(err, c.Project, "check enabled APIs", "roles/serviceusage.serviceUsageViewer")
		}
		if state.State != "ENABLED" {
			gaps = append(gaps, fmt.Sprintf("the %s API is disabled — enable it: gcloud services enable %s --project %s", api, api, c.Project))
		}
	}

	var granted struct {
		Permissions []string `json:"permissions"`
	}
	ask := make([]string, len(perms))
	for i, p := range perms {
		ask[i] = p.perm
	}
	url := fmt.Sprintf("https://cloudresourcemanager.googleapis.com/v1/projects/%s:testIamPermissions", c.Project)
	if err := c.do(ctx, http.MethodPost, url, map[string]any{"permissions": ask}, &granted); err != nil {
		return nil, Classify(err, c.Project, "check permissions", "roles/viewer")
	}
	have := map[string]bool{}
	for _, p := range granted.Permissions {
		have[p] = true
	}
	for _, p := range perms {
		if !have[p.perm] {
			gaps = append(gaps, fmt.Sprintf("you lack %s — grant: gcloud projects add-iam-policy-binding %s --member user:$(gcloud config get account) --role %s", p.perm, c.Project, p.role))
		}
	}

	// APIs and roles can be right while the Datastore database simply
	// doesn't exist — a distinct, fixable state with its own command.
	if needs.Datastore {
		probe := map[string]any{"keys": []any{map[string]any{
			"path": []map[string]any{{"kind": "tako-preflight", "name": "probe"}},
		}}}
		err := c.do(ctx, http.MethodPost, "https://datastore.googleapis.com/v1/projects/"+c.Project+":lookup", probe, nil)
		if ae, ok := err.(*apiError); ok && (ae.Status == http.StatusNotFound || strings.Contains(ae.Message, "database")) {
			gaps = append(gaps, fmt.Sprintf("no Datastore database in %s — create it: gcloud firestore databases create --location=%s --type=datastore-mode --project %s", c.Project, c.Region, c.Project))
		}
	}

	if len(gaps) > 0 {
		return nil, fmt.Errorf("preflight found %d gap(s):\n  %s", len(gaps), strings.Join(gaps, "\n  "))
	}
	short := make([]string, len(apis))
	for i, api := range apis {
		short[i] = strings.TrimSuffix(api, ".googleapis.com")
	}
	return short, nil
}

// ProjectNumber resolves the project's numeric id — deterministic Run
// URLs and the Pub/Sub service agent's email are derived from it.
func (c *Client) ProjectNumber(ctx context.Context) (string, error) {
	var out struct {
		ProjectNumber string `json:"projectNumber"`
	}
	url := "https://cloudresourcemanager.googleapis.com/v1/projects/" + c.Project
	if err := c.do(ctx, http.MethodGet, url, nil, &out); err != nil {
		return "", Classify(err, c.Project, "resolve the project number", "roles/viewer")
	}
	return out.ProjectNumber, nil
}
