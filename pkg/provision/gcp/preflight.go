package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// preflightAPIs are the services a C2 deploy touches.
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
}

// Preflight checks credentials, APIs, and permissions before any
// mutation — every gap is one line with its fix (preflight-as-UX).
func (c *Client) Preflight(ctx context.Context) error {
	var gaps []string

	for _, api := range preflightAPIs {
		var state struct {
			State string `json:"state"`
		}
		url := fmt.Sprintf("https://serviceusage.googleapis.com/v1/projects/%s/services/%s", c.Project, api)
		if err := c.do(ctx, http.MethodGet, url, nil, &state); err != nil {
			// Classify handles bad project / no auth; a serviceusage 403
			// means we cannot even ask — report and stop.
			return Classify(err, c.Project, "check enabled APIs", "roles/serviceusage.serviceUsageViewer")
		}
		if state.State != "ENABLED" {
			gaps = append(gaps, fmt.Sprintf("the %s API is disabled — enable it: gcloud services enable %s --project %s", api, api, c.Project))
		}
	}

	var granted struct {
		Permissions []string `json:"permissions"`
	}
	perms := make([]string, len(preflightPerms))
	for i, p := range preflightPerms {
		perms[i] = p.perm
	}
	url := fmt.Sprintf("https://cloudresourcemanager.googleapis.com/v1/projects/%s:testIamPermissions", c.Project)
	if err := c.do(ctx, http.MethodPost, url, map[string]any{"permissions": perms}, &granted); err != nil {
		return Classify(err, c.Project, "check permissions", "roles/viewer")
	}
	have := map[string]bool{}
	for _, p := range granted.Permissions {
		have[p] = true
	}
	for _, p := range preflightPerms {
		if !have[p.perm] {
			gaps = append(gaps, fmt.Sprintf("you lack %s — grant: gcloud projects add-iam-policy-binding %s --member user:$(gcloud config get account) --role %s", p.perm, c.Project, p.role))
		}
	}

	if len(gaps) > 0 {
		return fmt.Errorf("preflight found %d gap(s):\n  %s", len(gaps), strings.Join(gaps, "\n  "))
	}
	return nil
}
