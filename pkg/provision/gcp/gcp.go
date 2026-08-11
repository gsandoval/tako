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
	"os"
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
	firestoreHost  = "firestore.googleapis.com"
	billingHost    = "cloudbilling.googleapis.com"
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

// retryBackoff paces the retries below. Short on purpose: a real outage
// should surface as an error the user can act on, not as a command that
// appears to hang.
var retryBackoff = []time.Duration{time.Second, 3 * time.Second}

// readOnlyVerbs are Google's custom methods that READ despite being
// POSTs. Google's API convention puts custom methods after a colon
// (…/projects/p:testIamPermissions), and several of the most-called
// ones change nothing — so the method alone is too blunt a test for
// whether re-sending is safe.
//
// This list is deliberately an allowlist of verbs known to be reads,
// never a pattern: :commit, :setIamPolicy, :publish, :pull and
// :acknowledge are also colon-suffixed POSTs, and retrying any of them
// can duplicate work.
var readOnlyVerbs = []string{
	":testIamPermissions",  // preflight's permission probe
	":getIamPolicy",        // grant observation
	":runQuery",            // datastore reads
	":runAggregationQuery", // datastore counts
	":lookup",              // datastore key reads
	":list",                // logging entries:list, and AIP's list verb generally
	":batchGet",            // identitytoolkit account pages (AB10b): a POST-shaped read
}

// idempotentMutatingVerbs are the colon-suffixed POSTs that DO change
// something and are still safe to re-send, because Google defines them
// as returning the existing object rather than making a second one.
//
// They are a separate list rather than an entry in readOnlyVerbs
// because "changes nothing" and "safe to retry" are different claims,
// and only the second is what idempotent() asks. Filing an idempotent
// mutation as a read would make the read list a lie, and filing it as a
// mutation would deny a retry that is safe — so the argument for each
// member is recorded here, one at a time.
var idempotentMutatingVerbs = []string{
	// Provisions a Google service agent and returns the existing one if
	// it is already there (identity.go), which is why the deploy calls
	// it unconditionally instead of checking first. serviceusage is
	// exactly the kind of API that answers 429, and this call sits in
	// the deploy's critical path.
	":generateServiceIdentity",
	// identitytoolkit accounts:update (AB10b) SETS fields to the values
	// in the request — display name, disabled, the whole custom-claims
	// blob — so re-sending writes the same thing again. The read half of
	// the read-modify-write is what makes a role change safe, and that
	// happens above this layer, under a lease.
	":update",
	// accounts:delete of an account already gone answers an error the
	// store maps to success, so a retry lands in the same place a first
	// success would.
	":delete",
	// services:batchEnable asks for a set of APIs to be ON. Enabling one
	// already enabled is a no-op, so re-sending converges on the same
	// state — and this is the call a flaky first setup most wants
	// retried, since everything after it fails citing a disabled API.
	":batchEnable",
}

// mutatingVerbs are the colon-suffixed POSTs that must NOT be retried,
// listed so the completeness test can tell "classified as mutating"
// apart from "nobody has looked at it yet". A retried :commit writes
// twice; a retried :setIamPolicy can clobber a concurrent change.
var mutatingVerbs = []string{
	":commit",
	":setIamPolicy",
	":initializeAuth",
	":publish",     // a retried publish is a duplicate message
	":pull",        // leases messages and moves ack deadlines
	":acknowledge", // effectively idempotent, but not worth the reasoning
	// accounts:signUp MAKES an account (AB10b). A retry after a response
	// tako never saw would make a second one under a second id, and the
	// duplicate-address check would not catch it because the first write
	// did succeed. Not retried, deliberately.
	":signUp",
	// secrets:addVersion adds one every time it is called (SC4). A retry
	// after a response tako never saw would leave two versions holding
	// the same value — harmless to read, since services take the latest,
	// but it puts a phantom entry in the audit trail a person will later
	// try to explain. The operator re-runs if it failed.
	":addVersion",
}

// idempotent reports whether re-sending this request is safe. GET and
// DELETE are by definition; PUT is how the Pub/Sub APIs express "make
// this exist with this shape", which is also safe to repeat. A POST is
// not — a retried create can make two of something — UNLESS its URL
// names one of the custom methods above that reads, or mutates
// idempotently.
//
// That exception is not a nicety. The 2026-08-02 X2 release walk died
// in preflight on `429 Quota exceeded for quota metric 'Read requests'`
// from cloudresourcemanager: a rate limit on a call that reads,
// answered by killing a deploy that had not touched anything yet.
// Method-only idempotency made the single most retry-worthy call in the
// whole run the one call that could not retry.
func idempotent(method, url string) bool {
	if method == http.MethodGet || method == http.MethodDelete || method == http.MethodPut {
		return true
	}
	if method != http.MethodPost {
		return false
	}
	for _, verb := range readOnlyVerbs {
		if strings.HasSuffix(url, verb) {
			return true
		}
	}
	for _, verb := range idempotentMutatingVerbs {
		if strings.HasSuffix(url, verb) {
			return true
		}
	}
	return false
}

// do runs one JSON call. A nil out discards the response body.
//
// Transient failures — 5xx, 429, and connection errors — are retried for
// idempotent methods. Reconciliation converges, so a failed run has
// always been fixable by running it again; the 2026-08-02 release walk
// hit a one-off `503 from pubsub.googleapis.com` deleting a
// subscription, which stopped a destroy halfway and left the user to
// discover that re-running finishes it. Converging is the design;
// making the user drive it for a blip is not.
func (c *Client) do(ctx context.Context, method, url string, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if in != nil {
			// A fresh reader per attempt: the previous one is consumed.
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			return err
		}
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		// Bill this call to the project tako was GIVEN, not to whatever
		// project minted the credentials.
		//
		// Application Default Credentials from `gcloud auth
		// application-default login` carry no quota project, so Google
		// falls back to the project behind gcloud's own shared OAuth
		// client — shared with every gcloud user on earth, and
		// rate-limited accordingly. Reads are where that bites: a first
		// `tako logs` after a deploy met HTTP 429 for a consumer that
		// was not the target project, while `gcloud logging read`
		// against the same project worked, because gcloud bills its own
		// reads correctly. tako is told --project for everything else it
		// does here; this is that same answer, in the header Google
		// reads it from.
		//
		// It costs a permission — serviceusage.services.use on the
		// target — which the deployer already needs for the preflight's
		// enabled-API check and for provisioning service agents. A
		// credential that cannot set this cannot have reached this code.
		req.Header.Set("X-Goog-User-Project", c.Project)
		res, err := c.HC.Do(req)
		if err == nil {
			raw, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode/100 == 2 {
				if out != nil && len(raw) > 0 {
					return json.Unmarshal(raw, out)
				}
				return nil
			}
			if !transientStatus(res.StatusCode) || !idempotent(method, url) || attempt >= len(retryBackoff) {
				return parseAPIError(res.StatusCode, url, raw)
			}
		} else if !idempotent(method, url) || attempt >= len(retryBackoff) {
			return err
		}
		// A silent retry hides degradation: if an API is having a bad
		// afternoon, the deploy that took four minutes instead of two
		// should say why. One line, to stderr, so it never lands in
		// piped plan output.
		fmt.Fprintf(os.Stderr, "  ↻ %s %s: %s — retrying in %s\n",
			method, lastSegment(url), retryReason(res, err), retryBackoff[attempt])
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryBackoff[attempt]):
		}
	}
}

// retryReason names what we are waiting out, in the shortest honest way.
func retryReason(res *http.Response, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("HTTP %d", res.StatusCode)
}

// transientStatus reports the answers worth trying again: the server
// said it could not do this NOW, not that it will not.
func transientStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
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
	case strings.Contains(ae.Message, "Billing must be enabled") || strings.Contains(ae.Message, "Billing account for project"):
		// A project with no billing account refuses to enable Cloud Run,
		// Artifact Registry and Datastore — and says so as a 400 naming
		// services the user never mentioned. It is the first failure a
		// brand new project produces, and without its own text it reads
		// like a tako bug or a permissions problem. (Found by running
		// setup against a fresh project, SC2 2026-08-10.)
		return fmt.Errorf("%s has no billing account — Cloud Run, Artifact Registry and\n"+
			"      Datastore all refuse without one, and every failure after this would\n"+
			"      name something else. Link one, then re-run:\n"+
			"        https://console.cloud.google.com/billing/linkedaccount?project=%s",
			project, project)
	case ae.Status == http.StatusForbidden && strings.Contains(ae.Message, "serviceusage.services.use"):
		// Every call tako makes names the target project as the quota
		// project, so reads are metered against it rather than against
		// gcloud's shared OAuth client. That needs one permission, and
		// this is what its absence looks like — worth its own text,
		// because the denial names a service nobody asked tako to touch
		// and reads like a bug in tako rather than a missing grant.
		return fmt.Errorf("cannot bill API calls to %s — grant: gcloud projects add-iam-policy-binding %s --member user:$(gcloud config get account) --role roles/serviceusage.serviceUsageConsumer", project, project)
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
