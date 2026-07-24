package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gsandoval/tako/pkg/provision"
)

// ServiceAccounts manages per-service identities. GCP service accounts
// carry no labels, so tako's markers ride the description field:
// "managed by tako | app=<app> env=<env> | spec-hash=<hex>".
type ServiceAccounts struct {
	C   *Client
	App string
	Env string
}

func (s *ServiceAccounts) Kind() provision.Kind { return provision.KindServiceAccount }

func (s *ServiceAccounts) marker() string {
	return fmt.Sprintf("managed by tako | app=%s env=%s", s.App, s.Env)
}

type saBody struct {
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}

func (s *ServiceAccounts) List(ctx context.Context) ([]provision.Observed, error) {
	var observed []provision.Observed
	pageToken := ""
	for {
		url := fmt.Sprintf("https://iam.googleapis.com/v1/projects/%s/serviceAccounts?pageSize=100&pageToken=%s", s.C.Project, pageToken)
		var out struct {
			Accounts      []saBody `json:"accounts"`
			NextPageToken string   `json:"nextPageToken"`
		}
		if err := s.C.do(ctx, http.MethodGet, url, nil, &out); err != nil {
			return nil, Classify(err, s.C.Project, "list service accounts", "roles/iam.serviceAccountViewer")
		}
		for _, sa := range out.Accounts {
			if !strings.HasPrefix(sa.Description, s.marker()) {
				continue
			}
			hash := ""
			if i := strings.Index(sa.Description, "spec-hash="); i >= 0 {
				hash = strings.TrimSpace(sa.Description[i+len("spec-hash="):])
			}
			observed = append(observed, provision.Observed{
				Kind:     provision.KindServiceAccount,
				Name:     strings.SplitN(sa.Email, "@", 2)[0],
				SpecHash: hash,
			})
		}
		if out.NextPageToken == "" {
			return observed, nil
		}
		pageToken = out.NextPageToken
	}
}

func (s *ServiceAccounts) description(o provision.Object) string {
	return fmt.Sprintf("%s | spec-hash=%s", s.marker(), provision.SpecHash(o.Spec))
}

func (s *ServiceAccounts) Create(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://iam.googleapis.com/v1/projects/%s/serviceAccounts", s.C.Project)
	body := map[string]any{
		"accountId": o.Name,
		"serviceAccount": saBody{
			DisplayName: fmt.Sprint(o.Spec["displayName"]),
			Description: s.description(o),
		},
	}
	if err := s.C.do(ctx, http.MethodPost, url, body, nil); err != nil {
		return Classify(err, s.C.Project, "create the service account", "roles/iam.serviceAccountAdmin")
	}
	return nil
}

func (s *ServiceAccounts) Update(ctx context.Context, o provision.Object) error {
	email := s.Email(o.Name)
	url := fmt.Sprintf("https://iam.googleapis.com/v1/projects/%s/serviceAccounts/%s?updateMask=displayName,description", s.C.Project, email)
	body := saBody{DisplayName: fmt.Sprint(o.Spec["displayName"]), Description: s.description(o)}
	if err := s.C.do(ctx, http.MethodPatch, url, body, nil); err != nil {
		return Classify(err, s.C.Project, "update the service account", "roles/iam.serviceAccountAdmin")
	}
	return nil
}

func (s *ServiceAccounts) Delete(ctx context.Context, name string) error {
	url := fmt.Sprintf("https://iam.googleapis.com/v1/projects/%s/serviceAccounts/%s", s.C.Project, s.Email(name))
	if err := s.C.do(ctx, http.MethodDelete, url, nil, nil); err != nil {
		return Classify(err, s.C.Project, "delete the service account", "roles/iam.serviceAccountAdmin")
	}
	return nil
}

// Email is the account's full address.
func (s *ServiceAccounts) Email(name string) string {
	return name + "@" + s.C.Project + ".iam.gserviceaccount.com"
}
