package gcp

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
	Website          map[string]any    `json:"website,omitempty"`
	Lifecycle        map[string]any    `json:"lifecycle,omitempty"`
}

func bucketLifecycleRule(ttlDays int) map[string]any {
	return map[string]any{
		"rule": []map[string]any{{
			"action":    map[string]any{"type": "Delete"},
			"condition": map[string]any{"age": ttlDays},
		}},
	}
}

func specInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

// corsForSignedTransfers lets browsers execute signed URLs directly
// (the point of SignedPut): permissive origins, transfer verbs only.
var corsForSignedTransfers = []map[string]any{{
	"origin":         []string{"*"},
	"method":         []string{"GET", "PUT", "HEAD"},
	"responseHeader": []string{"Content-Type", "X-Goog-Content-Length-Range"},
	"maxAgeSeconds":  3600,
}}

func (b *Buckets) List(ctx context.Context) ([]provision.Observed, error) {
	var observed []provision.Observed
	pageToken := ""
	for {
		u := b.C.url(storageHost, "/storage/v1/b?project=%s&pageToken=%s", b.C.Project, url.QueryEscape(pageToken))
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
			specHash := bk.Labels["tako-spec-hash"]
			if len(bk.Cors) > 0 && !corsHasSignedTransferHeaders(bk.Cors) {
				specHash = "stale-cors"
			}
			observed = append(observed, provision.Observed{
				Kind:     provision.KindBucket,
				Name:     bk.Name,
				SpecHash: specHash,
			})
		}
		if out.NextPageToken == "" {
			return observed, nil
		}
		pageToken = out.NextPageToken
	}
}

func corsHasSignedTransferHeaders(cors []map[string]any) bool {
	if len(cors) == 0 {
		return false
	}
	foundCT := false
	foundRange := false
	switch hdrs := cors[0]["responseHeader"].(type) {
	case []any:
		for _, h := range hdrs {
			s := fmt.Sprint(h)
			if strings.EqualFold(s, "Content-Type") {
				foundCT = true
			}
			if strings.EqualFold(s, "X-Goog-Content-Length-Range") {
				foundRange = true
			}
		}
	case []string:
		for _, s := range hdrs {
			if strings.EqualFold(s, "Content-Type") {
				foundCT = true
			}
			if strings.EqualFold(s, "X-Goog-Content-Length-Range") {
				foundRange = true
			}
		}
	}
	return foundCT && foundRange
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
	if website, _ := o.Spec["website"].(bool); website {
		body.Website = map[string]any{"mainPageSuffix": "index.html", "notFoundPage": "404.html"}
	}
	if ttlDays := specInt(o.Spec["ttlDays"]); ttlDays > 0 {
		body.Lifecycle = bucketLifecycleRule(ttlDays)
	}
	return body
}

func (b *Buckets) Create(ctx context.Context, o provision.Object) error {
	u := b.C.url(storageHost, "/storage/v1/b?project="+b.C.Project)
	if err := b.C.do(ctx, http.MethodPost, u, b.body(o), nil); err != nil {
		return Classify(err, b.C.Project, "create the bucket", "roles/storage.admin")
	}
	if pub, hasPub := o.Spec["public"].(bool); hasPub && pub {
		if err := b.setPublic(ctx, o.Name, true); err != nil {
			return err
		}
	}
	return nil
}

func (b *Buckets) Update(ctx context.Context, o provision.Object) error {
	u := b.C.url(storageHost, "/storage/v1/b/"+o.Name)
	body := map[string]any{"labels": withHash(b.Labels, provision.SpecHash(o.Spec))}
	if cors, _ := o.Spec["cors"].(bool); cors {
		body["cors"] = corsForSignedTransfers
	}
	if website, _ := o.Spec["website"].(bool); website {
		body["website"] = map[string]any{"mainPageSuffix": "index.html", "notFoundPage": "404.html"}
	}
	if ttlDays := specInt(o.Spec["ttlDays"]); ttlDays > 0 {
		body["lifecycle"] = bucketLifecycleRule(ttlDays)
	}
	if err := b.C.do(ctx, http.MethodPatch, u, body, nil); err != nil {
		return Classify(err, b.C.Project, "update the bucket", "roles/storage.admin")
	}
	if pub, hasPub := o.Spec["public"].(bool); hasPub {
		if err := b.setPublic(ctx, o.Name, pub); err != nil {
			return err
		}
	}
	return nil
}

func (b *Buckets) setPublic(ctx context.Context, bucket string, public bool) error {
	iamURL := b.C.url(storageHost, "/storage/v1/b/%s/iam", bucket)
	var live policy
	if err := b.C.do(ctx, http.MethodGet, iamURL, nil, &live); err != nil {
		return Classify(err, b.C.Project, "get bucket IAM policy", "roles/storage.admin")
	}
	next, changed := withPublicBucketViewer(live, public)
	if !changed {
		return nil
	}
	if err := b.C.do(ctx, http.MethodPut, iamURL, next, nil); err != nil {
		return Classify(err, b.C.Project, "set bucket IAM policy", "roles/storage.admin")
	}
	return nil
}

func withPublicBucketViewer(live policy, public bool) (policy, bool) {
	const role = "roles/storage.objectViewer"
	const member = "allUsers"
	has := false
	for _, b := range live.Bindings {
		if b.Role == role {
			for _, m := range b.Members {
				if m == member {
					has = true
				}
			}
		}
	}
	if has == public {
		return live, false
	}
	next := policy{Etag: live.Etag}
	added := false
	for _, b := range live.Bindings {
		if b.Role != role {
			next.Bindings = append(next.Bindings, b)
			continue
		}
		var ms []string
		for _, m := range b.Members {
			if m != member {
				ms = append(ms, m)
			}
		}
		if public {
			ms = append(ms, member)
			added = true
		}
		if len(ms) > 0 {
			b.Members = ms
			next.Bindings = append(next.Bindings, b)
		}
	}
	if public && !added {
		next.Bindings = append(next.Bindings, struct {
			Role    string   `json:"role"`
			Members []string `json:"members"`
		}{Role: role, Members: []string{member}})
	}
	return next, true
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
		u := b.C.url(storageHost, "/storage/v1/b/"+name+"/o?fields=items(name)")
		if err := b.C.do(ctx, http.MethodGet, u, nil, &page); err != nil {
			return Classify(err, b.C.Project, "list bucket objects", "roles/storage.admin")
		}
		if len(page.Items) == 0 {
			break
		}
		for _, it := range page.Items {
			du := b.C.url(storageHost, "/storage/v1/b/"+name+"/o/"+url.PathEscape(it.Name))
			if err := b.C.do(ctx, http.MethodDelete, du, nil, nil); err != nil {
				return Classify(err, b.C.Project, "delete bucket object", "roles/storage.admin")
			}
		}
	}
	if err := b.C.do(ctx, http.MethodDelete, b.C.url(storageHost, "/storage/v1/b/"+name), nil, nil); err != nil {
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
	u := c.url(storageHost, "/storage/v1/b/%s/o?fields=items(name)&maxResults=%d", bucket, max)
	if err := c.do(ctx, http.MethodGet, u, nil, &page); err != nil {
		return nil, Classify(err, c.Project, "list bucket objects", "roles/storage.admin")
	}
	names := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		names = append(names, it.Name)
	}
	return names, nil
}

// SyncDir uploads every file under dir into the named GCS bucket and removes
// any stale objects in the bucket that are not present in dir.
func (c *Client) SyncDir(ctx context.Context, bucket, dir string) (int, error) {
	uploaded := map[string]bool{}
	count := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		ct := "application/octet-stream"
		switch {
		case strings.HasSuffix(key, ".html"):
			ct = "text/html; charset=utf-8"
		case strings.HasSuffix(key, ".json"):
			ct = "application/json; charset=utf-8"
		case strings.HasSuffix(key, ".txt"):
			ct = "text/plain; charset=utf-8"
		}
		u := c.url(storageHost, "/upload/storage/v1/b/%s/o?uploadType=media&name=%s", bucket, url.QueryEscape(key))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", ct)
		resp, err := c.HC.Do(req)
		if err != nil {
			return Classify(err, c.Project, "upload docs object", "roles/storage.objectAdmin")
		}
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("upload %s to bucket %s: HTTP %d", key, bucket, resp.StatusCode)
		}
		uploaded[key] = true
		count++
		return nil
	})
	if err != nil {
		return count, err
	}

	// Remove stale objects from the bucket that are no longer in dir.
	existing, err := c.ListObjects(ctx, bucket, 1000)
	if err == nil {
		for _, name := range existing {
			if !uploaded[name] {
				du := c.url(storageHost, "/storage/v1/b/%s/o/%s", bucket, url.PathEscape(name))
				_ = c.do(ctx, http.MethodDelete, du, nil, nil)
			}
		}
	}
	return count, nil
}
