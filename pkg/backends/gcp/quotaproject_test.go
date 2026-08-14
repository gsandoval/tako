package gcp

// FL22: `tako users --project …` could not run for the credentials an
// operator has.
//
// ADC from `gcloud auth application-default login` carries no quota
// project, so Google bills the call to the project behind gcloud's own
// shared OAuth client, and identitytoolkit refuses outright rather than
// rate-limiting. FL19 fixed exactly this for the provisioner's client;
// `tako users` goes through THIS one, which sent no such header.
//
// The half that needs a test more than the fix does is the ABSENCE:
// this package is shared by the CLI and by every deployed service, and
// x-goog-user-project costs serviceusage.services.use — a permission a
// service's derived least-privilege SA deliberately does not have. Sent
// unconditionally, it would turn every runtime call in every deployed
// service into a 403. So the header is a user-credential thing, and the
// test that says so passes before this change and after it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// headerSpy answers everything with {} and records what it was sent.
func headerSpy(t *testing.T) (*httptest.Server, *[]http.Header) {
	t.Helper()
	var got []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestUserCredentialsBillTheTargetProject(t *testing.T) {
	srv, got := headerSpy(t)
	c := &Client{
		HC:           srv.Client(),
		Opts:         Options{Project: "tako-test-03", App: "flauta", Env: "prod"},
		Base:         srv.URL,
		quotaProject: "tako-test-03", // what New records for an authorized_user credential
	}
	if err := c.do(context.Background(), http.MethodGet, c.url(identityHost, "/v1/projects/p/accounts"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("got %d calls, want 1", len(*got))
	}
	if h := (*got)[0].Get("X-Goog-User-Project"); h != "tako-test-03" {
		t.Errorf("X-Goog-User-Project = %q, want the target project — without it Google bills\n"+
			"the call to gcloud's shared OAuth client and identitytoolkit refuses it", h)
	}
}

// The regression guard. A deployed service authenticates from the
// metadata server, bills correctly already, and holds only the grants
// its package's code demanded.
func TestServiceCredentialsSendNoQuotaHeader(t *testing.T) {
	srv, got := headerSpy(t)
	c := &Client{
		HC:   srv.Client(),
		Opts: Options{Project: "tako-test-03", App: "flauta", Env: "prod"},
		Base: srv.URL,
		// quotaProject empty: metadata and service-account credentials
		// carry no credential JSON for New to recognize.
	}
	if err := c.do(context.Background(), http.MethodGet, c.url(datastoreHost, "/v1/projects/p:runQuery"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if h := (*got)[0].Get("X-Goog-User-Project"); h != "" {
		t.Errorf("a deployed service sent X-Goog-User-Project=%q — the header costs\n"+
			"serviceusage.services.use, which a derived least-privilege service account does\n"+
			"not have, so every runtime call would answer 403", h)
	}
}

// quotaProjectOf is the whole credential-type decision, and it is worth
// testing on the JSON rather than on a live credential: the shapes are
// Google's, stable, and the wrong answer in either direction is a broken
// deployment or a broken CLI.
func TestOnlyUserCredentialsGetAQuotaProject(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		want string
	}{
		{"gcloud ADC", `{"type":"authorized_user","client_id":"32555940559.apps.googleusercontent.com"}`, "tako-test-03"},
		{"service account key", `{"type":"service_account","client_email":"x@p.iam.gserviceaccount.com"}`, ""},
		{"impersonated service account", `{"type":"impersonated_service_account"}`, ""},
		{"metadata server (no JSON at all)", ``, ""},
		{"not JSON", `<html>`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := quotaProjectOf([]byte(tc.json), "tako-test-03"); got != tc.want {
				t.Errorf("quotaProjectOf(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// The store's own classification, which a deployed service's logs get
// (the CLI has its own copy of this ladder, tested in cmd/tako). The
// two billing branches have to come BEFORE the generic 403: they wear
// the same status, and the generic one names a role that changes
// nothing about either.
func TestBillingRefusalsAreNotReportedAsAMissingRole(t *testing.T) {
	s := &UserStore{c: &Client{Opts: Options{Project: "tako-test-03"}}}
	for _, tc := range []struct{ name, body, want string }{
		{"no quota project", "The identitytoolkit.googleapis.com API requires a quota project, which is not set by default.",
			"needs a billing project on tako-test-03"},
		{"cannot bill", "Permission denied on serviceusage.services.use for project tako-test-03",
			"roles/serviceusage.serviceUsageConsumer"},
		{"genuinely a role", "caller does not have permission",
			"auth.Users() derives roles/identitytoolkit.admin"},
		{"API off", "SERVICE_DISABLED", "the Identity Platform API is not enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := s.explain(&statusError{Code: http.StatusForbidden, Body: tc.body, URL: "/v1/projects/p/accounts"}, "list accounts")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("body %q →\n%v\nwant it to mention %q", tc.body, err, tc.want)
			}
		})
	}
}
