package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// FlagStore implements backends.Flags locally, persisting rules in
// <dir>/flags.json and serving immutable FlagSnapshots.
type FlagStore struct {
	path string

	mu      sync.Mutex
	gen     int64
	modTime time.Time
	rules   map[string]backends.FlagRule
	snap    atomic.Pointer[backends.FlagSnapshot]
}

// NewFlagStore opens a local flag store rooted at dir (in-memory only when
// dir is empty).
func NewFlagStore(dir string) *FlagStore {
	var p string
	if dir != "" {
		p = filepath.Join(dir, "flags.json")
	}
	s := &FlagStore{
		path:  p,
		rules: map[string]backends.FlagRule{},
	}
	s.loadFromDiskLocked()
	s.publishSnapshotLocked()
	return s
}

func (s *FlagStore) loadFromDiskLocked() {
	if s.path == "" {
		return
	}
	info, err := os.Stat(s.path)
	if err != nil {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var doc struct {
		Generation int64                        `json:"generation"`
		Rules      map[string]backends.FlagRule `json:"rules"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return
	}
	if doc.Rules == nil {
		doc.Rules = map[string]backends.FlagRule{}
	}
	s.gen = doc.Generation
	s.rules = doc.Rules
	s.modTime = info.ModTime()
}

func (s *FlagStore) saveToDiskLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	doc := struct {
		Generation int64                        `json:"generation"`
		Rules      map[string]backends.FlagRule `json:"rules"`
	}{
		Generation: s.gen,
		Rules:      s.rules,
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if info, err := os.Stat(s.path); err == nil {
		s.modTime = info.ModTime()
	}
	return nil
}

func (s *FlagStore) publishSnapshotLocked() {
	s.snap.Store(backends.NewFlagSnapshot(s.gen, s.rules))
}

func (s *FlagStore) refreshIfDiskChanged() {
	if s.path == "" {
		return
	}
	info, err := os.Stat(s.path)
	if err != nil {
		return
	}
	s.mu.Lock()
	if !info.ModTime().Equal(s.modTime) {
		s.loadFromDiskLocked()
		s.publishSnapshotLocked()
	}
	s.mu.Unlock()
}

// Snapshot returns the latest immutable FlagSnapshot.
func (s *FlagStore) Snapshot() *backends.FlagSnapshot {
	s.refreshIfDiskChanged()
	if snap := s.snap.Load(); snap != nil {
		return snap
	}
	return backends.NewFlagSnapshot(0, nil)
}

// List returns all active FlagRules sorted by name.
func (s *FlagStore) List(_ context.Context) ([]backends.FlagRule, error) {
	snap := s.Snapshot()
	out := make([]backends.FlagRule, 0, len(snap.Rules))
	for _, r := range snap.Rules {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Put creates or updates a FlagRule and advances the snapshot generation.
func (s *FlagStore) Put(_ context.Context, rule backends.FlagRule) error {
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		s.loadFromDiskLocked()
	}
	s.gen++
	s.rules[rule.Name] = rule
	if err := s.saveToDiskLocked(); err != nil {
		return err
	}
	s.publishSnapshotLocked()
	return nil
}

// Delete removes a FlagRule.
func (s *FlagStore) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		s.loadFromDiskLocked()
	}
	delete(s.rules, name)
	s.gen++
	if err := s.saveToDiskLocked(); err != nil {
		return err
	}
	s.publishSnapshotLocked()
	return nil
}
