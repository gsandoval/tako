package gcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gsandoval/tako/pkg/provision"
)

// Repos manages Artifact Registry docker repositories. The spec hash
// and tako labels live in the repo's labels.
type Repos struct {
	C      *Client
	Labels map[string]string
}

func (r *Repos) Kind() provision.Kind { return provision.KindRepo }

type repoBody struct {
	Name   string            `json:"name,omitempty"`
	Format string            `json:"format"`
	Labels map[string]string `json:"labels"`
}

func (r *Repos) List(ctx context.Context) ([]provision.Observed, error) {
	url := fmt.Sprintf("https://artifactregistry.googleapis.com/v1/projects/%s/locations/%s/repositories?pageSize=1000",
		r.C.Project, r.C.Region)
	var out struct {
		Repositories []repoBody `json:"repositories"`
	}
	if err := r.C.do(ctx, http.MethodGet, url, nil, &out); err != nil {
		return nil, Classify(err, r.C.Project, "list artifact repos", "roles/artifactregistry.reader")
	}
	var observed []provision.Observed
	for _, repo := range out.Repositories {
		if !hasLabels(repo.Labels, r.Labels) {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind:     provision.KindRepo,
			Name:     lastSegment(repo.Name),
			SpecHash: repo.Labels["tako-spec-hash"],
		})
	}
	return observed, nil
}

func (r *Repos) Create(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://artifactregistry.googleapis.com/v1/projects/%s/locations/%s/repositories?repositoryId=%s",
		r.C.Project, r.C.Region, o.Name)
	body := repoBody{Format: "DOCKER", Labels: withHash(r.Labels, provision.SpecHash(o.Spec))}
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodPost, url, body, &op); err != nil {
		return Classify(err, r.C.Project, "create the artifact repo", "roles/artifactregistry.admin")
	}
	return r.C.waitOperation(ctx, "https://artifactregistry.googleapis.com/v1/"+op.Name, waitLong)
}

func (r *Repos) Update(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://artifactregistry.googleapis.com/v1/projects/%s/locations/%s/repositories/%s?updateMask=labels",
		r.C.Project, r.C.Region, o.Name)
	body := repoBody{Format: "DOCKER", Labels: withHash(r.Labels, provision.SpecHash(o.Spec))}
	if err := r.C.do(ctx, http.MethodPatch, url, body, nil); err != nil {
		return Classify(err, r.C.Project, "update the artifact repo", "roles/artifactregistry.admin")
	}
	return nil
}

func (r *Repos) Delete(ctx context.Context, name string) error {
	url := fmt.Sprintf("https://artifactregistry.googleapis.com/v1/projects/%s/locations/%s/repositories/%s",
		r.C.Project, r.C.Region, name)
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodDelete, url, nil, &op); err != nil {
		return Classify(err, r.C.Project, "delete the artifact repo", "roles/artifactregistry.admin")
	}
	return r.C.waitOperation(ctx, "https://artifactregistry.googleapis.com/v1/"+op.Name, waitLong)
}
