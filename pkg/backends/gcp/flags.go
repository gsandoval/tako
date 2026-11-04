package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// FlagStore implements backends.Flags over the project's framework bucket
// (<project>-tako), storing the environment's flag manifest at
// flags/<app>-<env>.json.
//
// Reads in running services are 100% in-memory from an atomic FlagSnapshot
// refreshed in the background with If-None-Match ETag polling; writes use
// GCS ifGenerationMatch compare-and-swap so concurrent CLI updates never
// clobber each other.
type FlagStore struct {
	c *Client

	mu       sync.Mutex
	etag     string
	stopOnce sync.Once
	stopCh   chan struct{}
	snap     atomic.Pointer[backends.FlagSnapshot]
}

// NewFlagStore builds a GCP FlagStore and performs an initial best-effort
// load. When pollInterval > 0, it starts a background ETag poller.
func NewFlagStore(c *Client, pollInterval time.Duration) *FlagStore {
	s := &FlagStore{
		c:      c,
		stopCh: make(chan struct{}),
	}
	s.snap.Store(backends.NewFlagSnapshot(0, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = s.Refresh(ctx)
	cancel()
	if pollInterval > 0 {
		go s.pollLoop(pollInterval)
	}
	return s
}

func (s *FlagStore) bucket() string { return s.c.Opts.Project + "-tako" }

func (s *FlagStore) object() string {
	return "flags/" + s.c.Opts.App + "-" + s.c.Opts.Env + ".json"
}

type flagManifest struct {
	Rules map[string]backends.FlagRule `json:"rules"`
}

func (s *FlagStore) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.Refresh(ctx)
			cancel()
		case <-s.stopCh:
			return
		}
	}
}

// Close stops the background ETag poller.
func (s *FlagStore) Close() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// Refresh polls the manifest from GCS with If-None-Match and atomically
// updates the in-memory FlagSnapshot if the generation changed.
func (s *FlagStore) Refresh(ctx context.Context) error {
	s.mu.Lock()
	etag := s.etag
	s.mu.Unlock()

	m, gen, newETag, modified, err := s.read(ctx, etag)
	if err != nil || !modified {
		return err
	}
	s.mu.Lock()
	s.etag = newETag
	s.mu.Unlock()
	s.snap.Store(backends.NewFlagSnapshot(gen, m.Rules))
	return nil
}

func (s *FlagStore) read(ctx context.Context, ifNoneMatch string) (manifest flagManifest, generation int64, etag string, modified bool, err error) {
	u := s.c.url(storageHost, "/storage/v1/b/"+s.bucket()+
		"/o/"+url.PathEscape(s.object())+"?alt=media")
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return flagManifest{}, 0, "", false, err
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := s.c.HC.Do(req)
	if err != nil {
		return flagManifest{}, 0, "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return flagManifest{}, 0, ifNoneMatch, false, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return flagManifest{Rules: map[string]backends.FlagRule{}}, 0, "", true, nil
	}
	if resp.StatusCode/100 != 2 {
		return flagManifest{}, 0, "", false, &statusError{Code: resp.StatusCode, Body: "flag manifest read failed", URL: req.URL.Path}
	}
	generation, _ = strconv.ParseInt(resp.Header.Get("x-goog-generation"), 10, 64)
	etag = resp.Header.Get("ETag")
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return flagManifest{}, 0, "", false, err
	}
	if manifest.Rules == nil {
		manifest.Rules = map[string]backends.FlagRule{}
	}
	return manifest, generation, etag, true, nil
}

func (s *FlagStore) write(ctx context.Context, m flagManifest, ifGeneration int64) (ok bool, generation int64, etag string, err error) {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, 0, "", err
	}
	u := s.c.url(storageHost, "/upload/storage/v1/b/"+s.bucket()+
		"/o?uploadType=media&name="+url.QueryEscape(s.object())+
		"&ifGenerationMatch="+strconv.FormatInt(ifGeneration, 10))
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(raw))
	if err != nil {
		return false, 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.c.HC.Do(req)
	if err != nil {
		return false, 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPreconditionFailed || resp.StatusCode == http.StatusConflict {
		return false, 0, "", nil
	}
	if resp.StatusCode/100 != 2 {
		return false, 0, "", &statusError{Code: resp.StatusCode, Body: "flag manifest write failed", URL: req.URL.Path}
	}
	var meta struct {
		Generation string `json:"generation"`
		ETag       string `json:"etag"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return false, 0, "", err
	}
	generation, _ = strconv.ParseInt(meta.Generation, 10, 64)
	return true, generation, meta.ETag, nil
}

// Snapshot returns the current in-memory FlagSnapshot.
func (s *FlagStore) Snapshot() *backends.FlagSnapshot {
	if snap := s.snap.Load(); snap != nil {
		return snap
	}
	return backends.NewFlagSnapshot(0, nil)
}

// List reads the latest manifest from GCS and returns all FlagRules sorted by name.
func (s *FlagStore) List(ctx context.Context) ([]backends.FlagRule, error) {
	if err := s.Refresh(ctx); err != nil {
		return nil, err
	}
	snap := s.Snapshot()
	out := make([]backends.FlagRule, 0, len(snap.Rules))
	for _, r := range snap.Rules {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Put atomically updates rule in the GCS manifest using ifGenerationMatch CAS.
func (s *FlagStore) Put(ctx context.Context, rule backends.FlagRule) error {
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = time.Now().UTC()
	}
	return s.mutate(ctx, func(m *flagManifest) {
		m.Rules[rule.Name] = rule
	})
}

// Delete atomically removes name from the GCS manifest using ifGenerationMatch CAS.
func (s *FlagStore) Delete(ctx context.Context, name string) error {
	return s.mutate(ctx, func(m *flagManifest) {
		delete(m.Rules, name)
	})
}

func (s *FlagStore) mutate(ctx context.Context, fn func(*flagManifest)) error {
	for range 8 {
		m, gen, _, _, err := s.read(ctx, "")
		if err != nil {
			return err
		}
		fn(&m)
		ok, newGen, newETag, err := s.write(ctx, m, gen)
		if err != nil {
			return err
		}
		if ok {
			s.mu.Lock()
			s.etag = newETag
			s.mu.Unlock()
			s.snap.Store(backends.NewFlagSnapshot(newGen, m.Rules))
			return nil
		}
	}
	return &statusError{Code: http.StatusConflict, Body: "flag manifest CAS contention", URL: s.object()}
}

var _ backends.Flags = (*FlagStore)(nil)
