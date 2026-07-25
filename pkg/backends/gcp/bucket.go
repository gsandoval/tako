package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// BucketStore implements backends.Bucket on GCS. Logical bucket names
// map to physical ones via BucketName (project + app scoped, env
// prefixed) — the same rule the provisioner creates them with.
type BucketStore struct {
	c *Client
}

func NewBucketStore(c *Client) *BucketStore { return &BucketStore{c: c} }

func (s *BucketStore) physical(bucket string) string {
	return BucketName(s.c.Opts.Project, s.c.Opts.App, s.c.Opts.Env, bucket)
}

func (s *BucketStore) objectURL(bucket, key string) string {
	return "https://storage.googleapis.com/storage/v1/b/" + s.physical(bucket) + "/o/" + url.PathEscape(key)
}

type objectMeta struct {
	Name        string `json:"name"`
	Size        string `json:"size"` // GCS serializes int64 as string
	ContentType string `json:"contentType"`
	Updated     string `json:"updated"`
}

func (m objectMeta) toMeta() backends.ObjectMeta {
	var size int64
	fmt.Sscan(m.Size, &size)
	updated, _ := time.Parse(time.RFC3339, m.Updated)
	return backends.ObjectMeta{Key: m.Name, Size: size, ContentType: m.ContentType, Updated: updated}
}

func (s *BucketStore) Put(ctx context.Context, bucket, key string, data backends.BlobData) error {
	u := "https://storage.googleapis.com/upload/storage/v1/b/" + s.physical(bucket) +
		"/o?uploadType=media&name=" + url.QueryEscape(key)
	req, err := http.NewRequestWithContext(ctx, "POST", u, data.Reader)
	if err != nil {
		return err
	}
	if data.ContentType != "" {
		req.Header.Set("Content-Type", data.ContentType)
	}
	resp, err := s.c.HC.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return &statusError{Code: resp.StatusCode, Body: "upload failed", URL: req.URL.Path}
	}
	// Bucket events lite: buckets with Uploaded subscribers announce
	// every successful through-service write on their events topic —
	// same ObjectMeta payload local's WireBucketEvents publishes. C5's
	// GCS-native notifications take over this topic (and cover signed
	// direct uploads) when those land.
	if s.c.Opts.BucketEvents[bucket] {
		var created objectMeta
		if json.NewDecoder(resp.Body).Decode(&created) != nil || created.Name == "" {
			created = objectMeta{Name: key, ContentType: data.ContentType}
		}
		payload, err := json.Marshal(created.toMeta())
		if err != nil {
			return err
		}
		if err := s.c.publish(ctx, EventTopicName(s.c.Opts.Env, bucket), payload, map[string]string{"tako-bucket": bucket}); err != nil {
			return fmt.Errorf("object stored but its Uploaded event did not publish: %w", err)
		}
	}
	return nil
}

func (s *BucketStore) Get(ctx context.Context, bucket, key string) (backends.BlobData, error) {
	var meta objectMeta
	if err := s.c.do(ctx, "GET", s.objectURL(bucket, key), nil, &meta); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return backends.BlobData{}, backends.ErrNotFound
		}
		return backends.BlobData{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.objectURL(bucket, key)+"?alt=media", nil)
	if err != nil {
		return backends.BlobData{}, err
	}
	resp, err := s.c.HC.Do(req)
	if err != nil {
		return backends.BlobData{}, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return backends.BlobData{}, backends.ErrNotFound
		}
		return backends.BlobData{}, &statusError{Code: resp.StatusCode, Body: "download failed", URL: req.URL.Path}
	}
	m := meta.toMeta()
	return backends.BlobData{Reader: resp.Body, ContentType: m.ContentType, Size: m.Size}, nil
}

func (s *BucketStore) Exists(ctx context.Context, bucket, key string) (bool, error) {
	err := s.c.do(ctx, "GET", s.objectURL(bucket, key), nil, &objectMeta{})
	if isStatus(err, http.StatusNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *BucketStore) Delete(ctx context.Context, bucket, key string) error {
	err := s.c.do(ctx, "DELETE", s.objectURL(bucket, key), nil, nil)
	if isStatus(err, http.StatusNotFound) {
		return nil // absent is not an error, per the contract
	}
	return err
}

func (s *BucketStore) List(ctx context.Context, bucket, prefix string) ([]backends.ObjectMeta, error) {
	var out []backends.ObjectMeta
	pageToken := ""
	for {
		u := "https://storage.googleapis.com/storage/v1/b/" + s.physical(bucket) +
			"/o?prefix=" + url.QueryEscape(prefix) + "&pageToken=" + url.QueryEscape(pageToken)
		var page struct {
			Items         []objectMeta `json:"items"`
			NextPageToken string       `json:"nextPageToken"`
		}
		if err := s.c.do(ctx, "GET", u, nil, &page); err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			out = append(out, it.toMeta())
		}
		if page.NextPageToken == "" {
			return out, nil // GCS lists in key order already
		}
		pageToken = page.NextPageToken
	}
}
