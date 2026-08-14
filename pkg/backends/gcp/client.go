package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Options configures the runtime backends for one deployed service.
type Options struct {
	Project string // GCP project id (TAKO_PROJECT)
	App     string // app name, scopes bucket names (TAKO_APP)
	Env     string // environment, the Datastore namespace (TAKO_ENV)
	Service string // this service's base name (TAKO_SERVICE) — dead-topic derivation
	// SignerEmail overrides the SA the Signer signs as; empty means
	// this service's own identity from the metadata server (the normal
	// Cloud Run case — conformance harnesses set it explicitly).
	SignerEmail string
}

// Google API hosts. Every call this package makes is built from one of
// these through Client.url, so a test can point the whole package at a
// fake server — the seam MetricsStore already had and nothing else did.
const (
	storageHost    = "storage.googleapis.com"
	pubsubHost     = "pubsub.googleapis.com"
	datastoreHost  = "datastore.googleapis.com"
	tasksHost      = "cloudtasks.googleapis.com"
	runHost        = "run.googleapis.com"
	secretHost     = "secretmanager.googleapis.com"
	iamCredsHost   = "iamcredentials.googleapis.com"
	monitoringHost = "monitoring.googleapis.com"
	identityHost   = "identitytoolkit.googleapis.com"
)

// Client is the shared authenticated HTTP client behind the stores.
type Client struct {
	HC   *http.Client
	Opts Options
	// Base redirects every API host at one server, for tests. Empty
	// (production's only value) means the real hosts.
	Base string
	// quotaProject is the project API calls are billed to, sent as
	// x-goog-user-project. It is set ONLY for user credentials — see
	// quotaProjectOf, which is where the reasoning lives.
	quotaProject string
}

// url builds one API URL. path is a format string appended to the host.
func (c *Client) url(host, path string, args ...any) string {
	if len(args) > 0 {
		path = fmt.Sprintf(path, args...)
	}
	if c.Base != "" {
		return c.Base + "/" + host + path
	}
	return "https://" + host + path
}

// New authenticates via ADC (on Cloud Run: the service's own SA).
func New(ctx context.Context, opts Options) (*Client, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("cannot authenticate to Google Cloud: %v", err)
	}
	return &Client{
		HC:           oauth2.NewClient(ctx, creds.TokenSource),
		Opts:         opts,
		quotaProject: quotaProjectOf(creds.JSON, opts.Project),
	}, nil
}

// quotaProjectOf decides whether this credential needs to be told which
// project to bill, and it answers yes for exactly one kind (FL22).
//
// A credential from `gcloud auth application-default login` carries no
// project of its own, so Google bills the call to the project behind
// gcloud's shared OAuth client — shared with every gcloud user on earth.
// Most APIs rate-limit that; identitytoolkit refuses it outright, which
// is how `tako users --project …` came to fail for every operator who
// followed the runbook. Setting quota_project_id in the credential file
// changes nothing on its own: it only matters if something sends it as
// x-goog-user-project, and nothing here did.
//
// It is NOT sent for anything else, and that asymmetry is the point.
// This package is the CLI's client AND every deployed service's client.
// The header costs serviceusage.services.use on the target, a permission
// a service's SA deliberately does not hold — grants here derive from
// the app graph, and no graph asks for that one. Sent unconditionally,
// this would answer 403 for every runtime call in every deployed
// service. A metadata credential (Cloud Run) has no JSON at all, and a
// service-account key already bills to its own project, so both are
// left alone.
func quotaProjectOf(credentialJSON []byte, project string) string {
	var c struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(credentialJSON, &c) != nil || c.Type != "authorized_user" {
		return ""
	}
	return project
}

// statusError is a non-2xx API answer. Callers translate 404s into the
// contract's ErrNotFound where absence is meaningful.
type statusError struct {
	Code int
	Body string
	URL  string
}

func (e *statusError) Error() string {
	if e.Code == http.StatusForbidden {
		return fmt.Sprintf("permission denied by %s — this service's grants derive from the app graph at deploy time, so a runtime 403 is a tako deploy bug: re-run tako deploy, and file it if it persists (%s)", e.URL, e.Body)
	}
	return fmt.Sprintf("HTTP %d from %s: %s", e.Code, e.URL, e.Body)
}

// do runs one JSON call; nil out discards the response.
func (c *Client) do(ctx context.Context, method, url string, in, out any) error {
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
	if c.quotaProject != "" {
		req.Header.Set("X-Goog-User-Project", c.quotaProject)
	}
	resp, err := c.HC.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return &statusError{Code: resp.StatusCode, Body: compact(raw), URL: req.URL.Path}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func compact(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	s := string(raw)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func isStatus(err error, code int) bool {
	se, ok := err.(*statusError)
	return ok && se.Code == code
}
