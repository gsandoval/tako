package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// LeaseStore is the GCS lease backend (spec §4.4): each lease is one
// object in the framework bucket, written with ifGenerationMatch — the
// atomic compare-and-swap GCS gives away for free. The object body is
// {owner, expires}; the object GENERATION is the fencing token. Same
// bucket, same trick as the deploy lease — different namespace
// (leases/run-… vs leases/deploy-…).
type LeaseStore struct {
	c *Client
}

// NewLeaseStore builds the lease backend over the shared client.
func NewLeaseStore(c *Client) *LeaseStore { return &LeaseStore{c: c} }

func (s *LeaseStore) bucket() string { return s.c.Opts.Project + "-tako" }

// object scopes lease names by app and env: pr-42's reindex never
// contends with prod's.
func (s *LeaseStore) object(name string) string {
	return "leases/run-" + s.c.Opts.App + "-" + s.c.Opts.Env + "-" + name
}

type leaseBody struct {
	Owner   string    `json:"owner"`
	Expires time.Time `json:"expires"`
}

// read returns the live record and its generation; held=false when the
// object is absent (generation 0 for the create CAS).
func (s *LeaseStore) read(ctx context.Context, name string) (body leaseBody, generation int64, exists bool, err error) {
	u := s.c.url(storageHost, "/storage/v1/b/"+s.bucket()+
		"/o/"+url.PathEscape(s.object(name))+"?alt=media")
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return leaseBody{}, 0, false, err
	}
	resp, err := s.c.HC.Do(req)
	if err != nil {
		return leaseBody{}, 0, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return leaseBody{}, 0, false, nil
	}
	if resp.StatusCode/100 != 2 {
		return leaseBody{}, 0, false, &statusError{Code: resp.StatusCode, Body: "lease read failed", URL: req.URL.Path}
	}
	generation, _ = strconv.ParseInt(resp.Header.Get("x-goog-generation"), 10, 64)
	err = json.NewDecoder(resp.Body).Decode(&body)
	return body, generation, true, err
}

// write CAS-writes the record at the given generation (0 = must not
// exist). ok=false on a lost race (412), with the new generation on
// success.
func (s *LeaseStore) write(ctx context.Context, name string, body leaseBody, ifGeneration int64) (ok bool, generation int64, err error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return false, 0, err
	}
	u := s.c.url(storageHost, "/upload/storage/v1/b/"+s.bucket()+
		"/o?uploadType=media&name="+url.QueryEscape(s.object(name))+
		"&ifGenerationMatch="+strconv.FormatInt(ifGeneration, 10))
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(raw))
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.c.HC.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPreconditionFailed || resp.StatusCode == http.StatusConflict {
		return false, 0, nil // someone else won the CAS
	}
	if resp.StatusCode/100 != 2 {
		return false, 0, &statusError{Code: resp.StatusCode, Body: "lease write failed", URL: req.URL.Path}
	}
	var meta struct {
		Generation string `json:"generation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return false, 0, err
	}
	generation, _ = strconv.ParseInt(meta.Generation, 10, 64)
	return true, generation, nil
}

func (s *LeaseStore) TryAcquire(ctx context.Context, name, owner string, ttl time.Duration) (bool, int64, error) {
	body, generation, exists, err := s.read(ctx, name)
	if err != nil {
		return false, 0, err
	}
	if exists && body.Expires.After(time.Now()) {
		return false, 0, nil // live — not reentrant, even for its holder
	}
	target := int64(0) // create-only when absent…
	if exists {
		target = generation // …replace the expired holder's exact record
	}
	return s.write(ctx, name, leaseBody{Owner: owner, Expires: time.Now().Add(ttl)}, target)
}

func (s *LeaseStore) Extend(ctx context.Context, name, owner string, token int64, ttl time.Duration) (bool, int64, error) {
	// The CAS on token IS the holder check; owner rides in the body for
	// operator inspection.
	return s.write(ctx, name, leaseBody{Owner: owner, Expires: time.Now().Add(ttl)}, token)
}

func (s *LeaseStore) Release(ctx context.Context, name, owner string, token int64) error {
	u := s.c.url(storageHost, "/storage/v1/b/"+s.bucket()+
		"/o/"+url.PathEscape(s.object(name))+
		"?ifGenerationMatch="+strconv.FormatInt(token, 10))
	err := s.c.do(ctx, "DELETE", u, nil, nil)
	if isStatus(err, http.StatusNotFound) || isStatus(err, http.StatusPreconditionFailed) {
		return nil // already gone, or someone else's now — both fine
	}
	return err
}

var _ backends.Lease = (*LeaseStore)(nil)
