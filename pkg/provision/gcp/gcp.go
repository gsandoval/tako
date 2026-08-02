// Package gcp holds the thin REST adapters behind the reconciler's
// client interfaces. Plain HTTP + ADC tokens — no per-product SDKs —
// so every failure is a naked status code the error taxonomy
// (docs/tako-cloud-permissions.md) can classify honestly.
package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
)

// Google API hosts. Every URL in this package is built from one of
// these through Client.url, so a test can point the whole package at a
// fake server and no adapter can quietly address a host of its own.
const (
	runHost        = "run.googleapis.com"
	iamHost        = "iam.googleapis.com"
	crmHost        = "cloudresourcemanager.googleapis.com"
	storageHost    = "storage.googleapis.com"
	pubsubHost     = "pubsub.googleapis.com"
	datastoreHost  = "datastore.googleapis.com"
	schedulerHost  = "cloudscheduler.googleapis.com"
	tasksHost      = "cloudtasks.googleapis.com"
	artifactHost   = "artifactregistry.googleapis.com"
	secretHost     = "secretmanager.googleapis.com"
	serviceuseHost = "serviceusage.googleapis.com"
	loggingHost    = "logging.googleapis.com"
	identityHost   = "identitytoolkit.googleapis.com"
	monitoringHost = "monitoring.googleapis.com"
	iamCredsHost   = "iamcredentials.googleapis.com"
)

// Client is one authenticated deploy target.
type Client struct {
	HC      *http.Client
	Project string
	Region  string
	// Base redirects every API host at one server, for tests. Empty (the
	// only value production ever has) means the real hosts. It exists
	// because this package had no seam at all: four failures in one
	// stage — a container port with no number, a metric filed under a
	// resource type that refuses writes — were findable only by
	// deploying, since nothing here could be pointed at a fake.
	Base string
}

// url builds one API URL. path is a format string appended to the host,
// so call sites read as the API's own documentation does.
func (c *Client) url(host, path string, args ...any) string {
	if len(args) > 0 {
		path = fmt.Sprintf(path, args...)
	}
	if c.Base != "" {
		// The host becomes the first path segment so ONE fake server can
		// serve every API and still assert which was addressed.
		return c.Base + "/" + host + path
	}
	return "https://" + host + path
}

// NewClient builds a client from ambient credentials (gcloud/ADC).
func NewClient(ctx context.Context, project, region string) (*Client, error) {
	hc, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("cannot authenticate to Google Cloud — run \"gcloud auth application-default login\" (or set GOOGLE_APPLICATION_CREDENTIALS)")
	}
	return &Client{HC: hc, Project: project, Region: region}, nil
}

// apiError is one classified GCP API failure.
type apiError struct {
	Status  int
	Reason  string // e.g. SERVICE_DISABLED
	Message string
	API     string // the api host that answered
}

func (e *apiError) Error() string { return e.Message }

// asAPIError recovers the classified failure from err. It uses
// errors.As rather than a type assertion so that a call site which
// wraps (%w) on its way up — provision.Apply already does — does not
// silently demote every 403 and 404 to an unclassified message.
func asAPIError(err error) (*apiError, bool) {
	var ae *apiError
	return ae, errors.As(err, &ae)
}

// notFound reports the 404 shape every observer treats as "not created
// yet" and every delete treats as "already gone" — the two halves of
// converging without a state file.
func notFound(err error) bool {
	ae, ok := asAPIError(err)
	return ok && ae.Status == http.StatusNotFound
}

// do runs one JSON call. A nil out discards the response body.
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
	res, err := c.HC.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode/100 != 2 {
		return parseAPIError(res.StatusCode, url, raw)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func parseAPIError(status int, url string, raw []byte) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &payload)
	reason := ""
	for _, d := range payload.Error.Details {
		if d.Reason != "" {
			reason = d.Reason
		}
	}
	msg := payload.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
		if strings.HasPrefix(msg, "<") {
			// Google answers wrong URL paths with a full HTML error
			// page; dumping it helps nobody.
			msg = fmt.Sprintf("HTTP %d with an HTML (non-JSON) error page — usually a malformed API URL", status)
		}
	}
	host := url
	if i := strings.Index(url, "://"); i >= 0 {
		host = url[i+3:]
	}
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	return &apiError{Status: status, Reason: reason, Message: msg, API: host}
}

// disabledAPI reports the SERVICE_DISABLED shape: an API that is off
// hosts nothing, so OBSERVATION treats it as an empty inventory —
// destroy must never demand an API the app never enabled (the T3 gate
// hit exactly this: a pre-Tasks app's destroy failing on cloudtasks).
func disabledAPI(err error) bool {
	ae, ok := asAPIError(err)
	return ok && ae.Status == http.StatusForbidden &&
		(ae.Reason == "SERVICE_DISABLED" || strings.Contains(ae.Message, "API has not been used") || strings.Contains(ae.Message, "it is disabled"))
}

// Classify maps an API failure onto the four-way taxonomy with a
// copy-pasteable remediation (the permission model's friction rule).
// verb names what tako was doing; role is the narrowest role that
// unblocks it.
func Classify(err error, project, verb, role string) error {
	ae, ok := asAPIError(err)
	if !ok {
		return err
	}
	switch {
	case ae.Status == http.StatusUnauthorized:
		return fmt.Errorf("cannot authenticate to Google Cloud — run \"gcloud auth application-default login\" (or set GOOGLE_APPLICATION_CREDENTIALS)")
	case ae.Status == http.StatusForbidden && (ae.Reason == "SERVICE_DISABLED" || strings.Contains(ae.Message, "API has not been used") || strings.Contains(ae.Message, "it is disabled")):
		api := apiFromMessage(ae)
		return fmt.Errorf("the %s API is disabled on %s — enable it: gcloud services enable %s --project %s", api, project, api, project)
	case ae.Status == http.StatusForbidden && strings.Contains(strings.ToLower(ae.Message), "policy"):
		return fmt.Errorf("%s denied by your organization's policy — tako will not try to work around it (see docs/tako-cloud-permissions.md): %s", verb, ae.Message)
	case ae.Status == http.StatusForbidden:
		return fmt.Errorf("you lack permission to %s on %s — grant: gcloud projects add-iam-policy-binding %s --member user:$(gcloud config get account) --role %s", verb, project, project, role)
	case ae.Status == http.StatusNotFound && strings.Contains(strings.ToLower(ae.Message), "project"):
		return fmt.Errorf("project %q not found (or you have no access) — check the id", project)
	}
	return fmt.Errorf("%s: %s (HTTP %d from %s)", verb, ae.Message, ae.Status, ae.API)
}

func apiFromMessage(ae *apiError) string {
	// Error messages name the service ("run.googleapis.com has not been
	// used…"); fall back to the answering host.
	for _, f := range strings.Fields(ae.Message) {
		if strings.HasSuffix(f, ".googleapis.com") {
			return strings.Trim(f, `".,`)
		}
	}
	return ae.API
}

// waitOperation polls a long-running operation until done.
func (c *Client) waitOperation(ctx context.Context, opURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var op struct {
			Done  bool `json:"done"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := c.do(ctx, http.MethodGet, opURL, nil, &op); err != nil {
			return err
		}
		if op.Done {
			if op.Error != nil {
				return fmt.Errorf("%s", op.Error.Message)
			}
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("operation did not finish within %s", timeout)
}
