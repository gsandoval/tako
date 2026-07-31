package gcp

import (
	"bytes"
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
	Scheduler bool // any cron declared (Cloud Scheduler triggers, T2)
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
	if needs.Scheduler {
		apis = append(apis, "cloudscheduler.googleapis.com")
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
func (c *Client) doIdentity(ctx context.Context, method, url string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
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
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// isNotFoundOrDenied matches the "Identity Platform not set up yet"
// answers (the API 404s or 403s CONFIGURATION_NOT_FOUND before
// initialization).
func isNotFoundOrDenied(err error) bool {
	ae, ok := err.(*apiError)
	return ok && (ae.Status == http.StatusNotFound || ae.Status == http.StatusForbidden)
}

// AuthClient is Identity Platform's browser client configuration plus
// the domain registrations sign-in popups require.
type AuthClient struct {
	APIKey     string
	AuthDomain string
}

// googleProviderSteps: the ONE genuinely manual step of the whole
// auth feature. Google's OAuth clients for external apps have no
// public creation API — but the Firebase console auto-provisions one
// behind a single toggle, so that is the primary instruction (the
// hand-built GCP path stays as the fallback for org-restricted
// consoles).
func googleProviderSteps(project string) string {
	return fmt.Sprintf("enable the Google provider (the one manual step): https://console.firebase.google.com/project/%s/authentication/providers → Google → Enable (pick a support email) → Save — the Firebase console auto-provisions the OAuth client. Manual alternative: APIs & Services → OAuth consent screen, then Credentials → OAuth client ID (Web application, redirect URI https://%s.firebaseapp.com/__/auth/handler), pasted into Identity Platform → Providers → Google", project, project)
}

// EnsureAuthClient makes the app's declared sign-in REAL, automating
// every mechanical piece and stopping only at the one trust/console
// step tako cannot do: it initializes Identity Platform when absent,
// enables the email provider when declared (the declaration IS the
// decision), verifies a declared Google provider carries a real OAuth
// client (the console accepts placeholders; Google then answers
// invalid_client at the first popup), reads the browser client config,
// and registers the given service hosts as authorized sign-in domains.
// mutate=false (tako deploy --plan) checks and narrates without
// touching anything.
func (c *Client) EnsureAuthClient(ctx context.Context, hosts, providers []string, mutate bool, say func(format string, args ...any)) (AuthClient, error) {
	base := "https://identitytoolkit.googleapis.com/admin/v2/projects/" + c.Project
	var config struct {
		SignIn struct {
			Email struct {
				Enabled bool `json:"enabled"`
			} `json:"email"`
		} `json:"signIn"`
		Client struct {
			APIKey            string `json:"apiKey"`
			FirebaseSubdomain string `json:"firebaseSubdomain"`
		} `json:"client"`
		AuthorizedDomains []string `json:"authorizedDomains"`
	}
	err := c.doIdentity(ctx, http.MethodGet, base+"/config", nil, &config)
	if isNotFoundOrDenied(err) {
		// Uninitialized: mechanical, automated.
		if !mutate {
			say("auth         would initialize Identity Platform (mechanical, done on a real deploy)")
			return AuthClient{}, nil
		}
		// initializeAuth lives on the /v2/ surface; only the config
		// resource is under /admin/v2/ (a wrong path gets Google's HTML
		// 404 page, not a JSON error).
		if err := c.doIdentity(ctx, http.MethodPost, "https://identitytoolkit.googleapis.com/v2/projects/"+c.Project+"/identityPlatform:initializeAuth", map[string]any{}, nil); err != nil {
			return AuthClient{}, fmt.Errorf("initializing Identity Platform: %w", err)
		}
		say("auth         initialized Identity Platform")
		if err := c.doIdentity(ctx, http.MethodGet, base+"/config", nil, &config); err != nil {
			return AuthClient{}, fmt.Errorf("reading the Identity Platform config after initialization: %w", err)
		}
	} else if err != nil {
		return AuthClient{}, fmt.Errorf("reading the Identity Platform client config: %w", err)
	}

	for _, p := range providers {
		switch p {
		case "email":
			if !config.SignIn.Email.Enabled {
				// Declared → enabled: the declaration is the decision.
				if !mutate {
					say("auth         would enable the email/password provider (declared)")
					continue
				}
				body := map[string]any{"signIn": map[string]any{"email": map[string]any{"enabled": true, "passwordRequired": true}}}
				if err := c.doIdentity(ctx, http.MethodPatch, base+"/config?updateMask=signIn.email", body, nil); err != nil {
					return AuthClient{}, fmt.Errorf("enabling the email provider: %w", err)
				}
				say("auth         email/password provider enabled (declared)")
			}
		case "google":
			var idps struct {
				Configs []struct {
					Name     string `json:"name"`
					Enabled  bool   `json:"enabled"`
					ClientID string `json:"clientId"`
				} `json:"defaultSupportedIdpConfigs"`
			}
			if err := c.doIdentity(ctx, http.MethodGet, base+"/defaultSupportedIdpConfigs", nil, &idps); err != nil {
				return AuthClient{}, fmt.Errorf("checking sign-in providers: %w", err)
			}
			enabled, clientID := false, ""
			for _, cfg := range idps.Configs {
				if strings.HasSuffix(cfg.Name, "google.com") && cfg.Enabled {
					enabled, clientID = true, cfg.ClientID
				}
			}
			switch {
			case !enabled:
				return AuthClient{}, fmt.Errorf("the Google sign-in provider is not configured in %s — %s", c.Project, googleProviderSteps(c.Project))
			case !strings.HasSuffix(clientID, ".apps.googleusercontent.com"):
				return AuthClient{}, fmt.Errorf("the Google provider in %s has client id %q, which is not a real OAuth web client — sign-in will fail with invalid_client; %s", c.Project, clientID, googleProviderSteps(c.Project))
			}
		}
	}

	if config.Client.APIKey == "" {
		return AuthClient{}, fmt.Errorf("Identity Platform has no browser API key in %s — initialization normally provisions one (console → Identity Platform → Application setup details)", c.Project)
	}
	have := map[string]bool{}
	var current []string
	for _, d := range config.AuthorizedDomains {
		if !have[d] { // dedupe defensively — a raw PATCH can double up
			have[d] = true
			current = append(current, d)
		}
	}
	missing := []string{}
	for _, h := range hosts {
		if !have[h] {
			have[h] = true
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 || len(current) != len(config.AuthorizedDomains) {
		if !mutate {
			for _, d := range missing {
				say("auth         would authorize %s for sign-in", d)
			}
		} else {
			domains := append(current, missing...)
			body := map[string]any{"authorizedDomains": domains}
			if err := c.doIdentity(ctx, http.MethodPatch, base+"/config?updateMask=authorizedDomains", body, nil); err != nil {
				return AuthClient{}, fmt.Errorf("authorizing sign-in domains: %w", err)
			}
			for _, d := range missing {
				say("auth         %s authorized for sign-in", d)
			}
		}
	}

	sub := config.Client.FirebaseSubdomain
	if sub == "" {
		sub = c.Project
	}
	return AuthClient{APIKey: config.Client.APIKey, AuthDomain: sub + ".firebaseapp.com"}, nil
}

// RemoveAuthorizedDomains unregisters hosts from sign-in — destroy's
// counterpart to the deploy's registration. Only ever called with
// tako-derived hosts; foreign domains are never touched.
func (c *Client) RemoveAuthorizedDomains(ctx context.Context, hosts []string, say func(format string, args ...any)) error {
	base := "https://identitytoolkit.googleapis.com/admin/v2/projects/" + c.Project
	var config struct {
		AuthorizedDomains []string `json:"authorizedDomains"`
	}
	if err := c.doIdentity(ctx, http.MethodGet, base+"/config", nil, &config); err != nil {
		if isNotFoundOrDenied(err) {
			return nil // no auth config: nothing registered
		}
		return err
	}
	drop := map[string]bool{}
	for _, h := range hosts {
		drop[h] = true
	}
	var keep []string
	removed := []string{}
	for _, d := range config.AuthorizedDomains {
		if drop[d] {
			removed = append(removed, d)
			continue
		}
		keep = append(keep, d)
	}
	if len(removed) == 0 {
		return nil
	}
	body := map[string]any{"authorizedDomains": keep}
	if err := c.doIdentity(ctx, http.MethodPatch, base+"/config?updateMask=authorizedDomains", body, nil); err != nil {
		return err
	}
	for _, d := range removed {
		if say != nil {
			say("✗ sign-in domain %s", d)
		}
	}
	return nil
}
