package gcp

import (
	"net/http"
	"strings"
	"testing"
)

// TestClassifyTaxonomy pins the four-way error taxonomy
// (docs/tako-cloud-permissions.md): unauthenticated, API disabled,
// org-policy, missing role, bad project — each with its own remediation.
func TestClassifyTaxonomy(t *testing.T) {
	const project = "my-proj"
	cases := []struct {
		name  string
		err   *apiError
		wants []string
	}{
		{
			"unauthenticated",
			&apiError{Status: http.StatusUnauthorized, Message: "Request had invalid authentication credentials."},
			[]string{"cannot authenticate", "gcloud auth application-default login"},
		},
		{
			"api disabled",
			&apiError{Status: http.StatusForbidden, Reason: "SERVICE_DISABLED",
				Message: "Cloud Run API has not been used in project my-proj before or it is disabled. Enable run.googleapis.com then retry."},
			[]string{"API is disabled", "gcloud services enable run.googleapis.com --project my-proj"},
		},
		{
			"org policy",
			&apiError{Status: http.StatusForbidden,
				Message: "One or more users named in the policy do not belong to a permitted customer (org policy constraint iam.allowedPolicyMemberDomains)."},
			[]string{"denied by your organization's policy", "will not try to work around it"},
		},
		{
			"missing role",
			&apiError{Status: http.StatusForbidden, Message: "Permission 'run.services.create' denied on resource."},
			[]string{"you lack permission to create the run service", "add-iam-policy-binding my-proj", "--role roles/run.admin"},
		},
		{
			"bad project",
			&apiError{Status: http.StatusNotFound, Message: "Project 'my-proj' not found or deleted."},
			[]string{`project "my-proj" not found`, "check the id"},
		},
	}
	for _, c := range cases {
		got := Classify(c.err, project, "create the run service", "roles/run.admin")
		for _, want := range c.wants {
			if !strings.Contains(got.Error(), want) {
				t.Fatalf("%s: error %q\n  missing %q", c.name, got, want)
			}
		}
	}
}

// TestParseAPIError pulls status/reason/message out of real-shaped GCP
// error bodies.
func TestParseAPIError(t *testing.T) {
	raw := []byte(`{"error":{"code":403,"message":"Cloud Run API has not been used","status":"PERMISSION_DENIED","details":[{"reason":"SERVICE_DISABLED"}]}}`)
	err := parseAPIError(403, "https://run.googleapis.com/v2/projects/x/locations/y/services", raw)
	ae, ok := err.(*apiError)
	if !ok || ae.Status != 403 || ae.Reason != "SERVICE_DISABLED" || ae.API != "run.googleapis.com" ||
		!strings.Contains(ae.Message, "has not been used") {
		t.Fatalf("parsed = %+v", err)
	}
}
