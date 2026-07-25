package gcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gsandoval/tako/pkg/provision"
)

// Buckets manages app GCS buckets (KindBucket). Labels carry the
// markers; names are already globally unique and physical.
type Buckets struct {
	C      *Client
	Labels map[string]string
}

func (b *Buckets) Kind() provision.Kind { return provision.KindBucket }

type bucketBody struct {
	Name             string            `json:"name,omitempty"`
	Location         string            `json:"location,omitempty"`
	Labels           map[string]string `json:"labels"`
	IamConfiguration map[string]any    `json:"iamConfiguration,omitempty"`
	Cors             []map[string]any  `json:"cors,omitempty"`
}

// corsForSignedTransfers lets browsers execute signed URLs directly
// (the point of SignedPut): permissive origins, transfer verbs only.
var corsForSignedTransfers = []map[string]any{{
	"origin":         []string{"*"},
	"method":         []string{"GET", "PUT", "HEAD"},
	"responseHeader": []string{"Content-Type"},
	"maxAgeSeconds":  3600,
}}

func (b *Buckets) List(ctx context.Context) ([]provision.Observed, error) {
	var observed []provision.Observed
	pageToken := ""
	for {
		u := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b?project=%s&pageToken=%s", b.C.Project, url.QueryEscape(pageToken))
		var out struct {
			Items         []bucketBody `json:"items"`
			NextPageToken string       `json:"nextPageToken"`
		}
		if err := b.C.do(ctx, http.MethodGet, u, nil, &out); err != nil {
			return nil, Classify(err, b.C.Project, "list buckets", "roles/storage.admin")
		}
		for _, bk := range out.Items {
			if !hasLabels(bk.Labels, b.Labels) {
				continue
			}
			// The framework lease bucket is labeled but owned by the
			// lease machinery (destroy deletes it last, deploys must
			// not orphan-report it).
			if bk.Name == b.C.Project+"-tako" {
				continue
			}
			observed = append(observed, provision.Observed{
				Kind:     provision.KindBucket,
				Name:     bk.Name,
				SpecHash: bk.Labels["tako-spec-hash"],
			})
		}
		if out.NextPageToken == "" {
			return observed, nil
		}
		pageToken = out.NextPageToken
	}
}

func (b *Buckets) body(o provision.Object) bucketBody {
	body := bucketBody{
		Name:     o.Name,
		Location: fmt.Sprint(o.Spec["location"]),
		Labels:   withHash(b.Labels, provision.SpecHash(o.Spec)),
	}
	if uniform, _ := o.Spec["uniform"].(bool); uniform {
		body.IamConfiguration = map[string]any{"uniformBucketLevelAccess": map[string]any{"enabled": true}}
	}
	if cors, _ := o.Spec["cors"].(bool); cors {
		body.Cors = corsForSignedTransfers
	}
	return body
}

func (b *Buckets) Create(ctx context.Context, o provision.Object) error {
	u := "https://storage.googleapis.com/storage/v1/b?project=" + b.C.Project
	if err := b.C.do(ctx, http.MethodPost, u, b.body(o), nil); err != nil {
		return Classify(err, b.C.Project, "create the bucket", "roles/storage.admin")
	}
	return nil
}

func (b *Buckets) Update(ctx context.Context, o provision.Object) error {
	u := "https://storage.googleapis.com/storage/v1/b/" + o.Name
	body := map[string]any{"labels": withHash(b.Labels, provision.SpecHash(o.Spec))}
	if cors, _ := o.Spec["cors"].(bool); cors {
		body["cors"] = corsForSignedTransfers
	}
	if err := b.C.do(ctx, http.MethodPatch, u, body, nil); err != nil {
		return Classify(err, b.C.Project, "update the bucket", "roles/storage.admin")
	}
	return nil
}

// Delete removes the bucket's objects, then the bucket. Only destroy
// calls this — and only after the §4.6a data confirmation.
func (b *Buckets) Delete(ctx context.Context, name string) error {
	for {
		var page struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
		}
		u := "https://storage.googleapis.com/storage/v1/b/" + name + "/o?fields=items(name)"
		if err := b.C.do(ctx, http.MethodGet, u, nil, &page); err != nil {
			return Classify(err, b.C.Project, "list bucket objects", "roles/storage.admin")
		}
		if len(page.Items) == 0 {
			break
		}
		for _, it := range page.Items {
			du := "https://storage.googleapis.com/storage/v1/b/" + name + "/o/" + url.PathEscape(it.Name)
			if err := b.C.do(ctx, http.MethodDelete, du, nil, nil); err != nil {
				return Classify(err, b.C.Project, "delete bucket object", "roles/storage.admin")
			}
		}
	}
	if err := b.C.do(ctx, http.MethodDelete, "https://storage.googleapis.com/storage/v1/b/"+name, nil, nil); err != nil {
		return Classify(err, b.C.Project, "delete the bucket", "roles/storage.admin")
	}
	return nil
}

// ListObjects returns up to max object names in a bucket — prune's
// confirmation listing (scale, not precision).
func (c *Client) ListObjects(ctx context.Context, bucket string, max int) ([]string, error) {
	var page struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	u := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s/o?fields=items(name)&maxResults=%d", bucket, max)
	if err := c.do(ctx, http.MethodGet, u, nil, &page); err != nil {
		return nil, Classify(err, c.Project, "list bucket objects", "roles/storage.admin")
	}
	names := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		names = append(names, it.Name)
	}
	return names, nil
}
