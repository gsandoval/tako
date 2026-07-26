package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	PubSub    bool // any queue, topic, or async handler declared
	// Secrets are the declared secrets' PHYSICAL (env-prefixed) names.
	// tako never creates secrets (values are data the user owns):
	// preflight verifies each exists and prints the exact create
	// command when one does not.
	Secrets []string
	// SignBlob is set when any handler mints signed URLs (sign edges):
	// the runtime calls the IAM Credentials API, which must be enabled.
	SignBlob bool
	// AuthProviders are the sign-in providers the app declares
	// (tako.Auth options): preflight verifies Identity Platform is
	// initialized and each declared provider configured, printing the
	// exact console steps when not — tako never mutates auth config
	// (the OAuth consent screen is genuinely interactive).
	AuthProviders []string
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
	if len(needs.Secrets) > 0 {
		apis = append(apis, "secretmanager.googleapis.com")
		perms = append(perms,
			struct{ perm, role string }{"secretmanager.secrets.get", "roles/secretmanager.viewer"},
			struct{ perm, role string }{"secretmanager.secrets.setIamPolicy", "roles/secretmanager.admin"})
	}
	if needs.SignBlob {
		apis = append(apis, "iamcredentials.googleapis.com")
	}
	if len(needs.AuthProviders) > 0 {
		apis = append(apis, "identitytoolkit.googleapis.com")
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

	// Declared secrets must exist before their services boot (boot
	// refuses to serve with one unset — the same contract as local's
	// .env). Missing is a distinct, fixable state with its own command.
	for _, physical := range needs.Secrets {
		u := "https://secretmanager.googleapis.com/v1/projects/" + c.Project + "/secrets/" + physical
		err := c.do(ctx, http.MethodGet, u, nil, nil)
		if ae, ok := err.(*apiError); ok && ae.Status == http.StatusNotFound {
			gaps = append(gaps, fmt.Sprintf("secret %s does not exist in %s — create it: printf '<value>' | gcloud secrets create %s --data-file=- --project %s", physical, c.Project, physical, c.Project))
		}
	}

	// Auth needs Identity Platform INITIALIZED and each declared
	// provider CONFIGURED — one-time console steps a deploy cannot do
	// for you (the OAuth consent screen is interactive). Without them
	// the app deploys but sign-in fails in the browser with no hint,
	// so absence is a preflight gap with the exact steps.
	if len(needs.AuthProviders) > 0 {
		gaps = append(gaps, c.authGaps(ctx, needs.AuthProviders)...)
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

// doIdentity runs one Identity Platform admin call. These endpoints
// demand a quota project on user ADC credentials (the
// x-goog-user-project header) — the recorded W5 gotcha.
func (c *Client) doIdentity(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-goog-user-project", c.Project)
	res, err := c.HC.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode/100 != 2 {
		return parseAPIError(res.StatusCode, url, raw)
	}
	return json.Unmarshal(raw, out)
}

// authGaps checks Identity Platform initialization and each declared
// provider, one actionable line per gap.
func (c *Client) authGaps(ctx context.Context, providers []string) []string {
	base := "https://identitytoolkit.googleapis.com/admin/v2/projects/" + c.Project
	var config struct {
		SignIn struct {
			Email struct {
				Enabled bool `json:"enabled"`
			} `json:"email"`
		} `json:"signIn"`
	}
	if err := c.doIdentity(ctx, base+"/config", &config); err != nil {
		if ae, ok := err.(*apiError); ok && (ae.Status == http.StatusNotFound || ae.Status == http.StatusForbidden) {
			return []string{fmt.Sprintf("Identity Platform is not set up in %s — console → Identity Platform → \"Get started\" (one click initializes it), then Providers → add each declared provider (%s)", c.Project, strings.Join(providers, ", "))}
		}
		return []string{fmt.Sprintf("cannot check Identity Platform config: %v", err)}
	}
	var gaps []string
	for _, p := range providers {
		switch p {
		case "google":
			var idps struct {
				Configs []struct {
					Name    string `json:"name"`
					Enabled bool   `json:"enabled"`
				} `json:"defaultSupportedIdpConfigs"`
			}
			if err := c.doIdentity(ctx, base+"/defaultSupportedIdpConfigs", &idps); err != nil {
				gaps = append(gaps, fmt.Sprintf("cannot check sign-in providers: %v", err))
				continue
			}
			enabled := false
			for _, cfg := range idps.Configs {
				if strings.HasSuffix(cfg.Name, "google.com") && cfg.Enabled {
					enabled = true
				}
			}
			if !enabled {
				gaps = append(gaps, fmt.Sprintf("the Google sign-in provider is not configured in %s — console → Identity Platform → Providers → add Google (accept the OAuth consent defaults); a one-time interactive step tako cannot do for you", c.Project))
			}
		case "email":
			if !config.SignIn.Email.Enabled {
				gaps = append(gaps, fmt.Sprintf("the email sign-in provider is not enabled in %s — console → Identity Platform → Providers → add Email/Password", c.Project))
			}
		}
	}
	return gaps
}
