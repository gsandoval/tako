package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// WireBucketEvents connects a bucket store's writes to the dispatcher's
// topics: every successful Put publishes the object's ObjectMeta (the
// tako.ObjectInfo wire shape) on the topic "<bucket>.Uploaded" — the
// local stand-in for GCS notifications (Phase F provides the same
// contract in the cloud).
func WireBucketEvents(s *BucketStore, d *QueueDispatcher) {
	s.OnPut(func(bucket string, meta backends.ObjectMeta) {
		payload, err := json.Marshal(meta)
		if err != nil {
			return
		}
		_ = d.Topics().Publish(context.Background(), bucket+".Uploaded", payload)
	})
}

// BucketStore implements the bucket contract on the filesystem: the file
// at <dir>/buckets/<bucket>/<key> IS the object, byte for byte, so dev
// data is directly inspectable. Content types live in a .meta/ sidecar
// directory beside the keys.
type BucketStore struct {
	dir string

	mu    sync.Mutex
	onPut []func(bucket string, meta backends.ObjectMeta)
}

// NewBucketStore roots a bucket store at dir (creating it lazily).
func NewBucketStore(dir string) *BucketStore {
	return &BucketStore{dir: dir}
}

// OnPut subscribes to successful writes — the local stand-in for GCS
// upload notifications. It fires for EVERY write, including a handler's
// own output.
func (s *BucketStore) OnPut(fn func(bucket string, meta backends.ObjectMeta)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onPut = append(s.onPut, fn)
}

func (s *BucketStore) objectPath(bucket, key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	return filepath.Join(s.dir, "buckets", bucket, filepath.FromSlash(key)), nil
}

func (s *BucketStore) metaPath(bucket, key string) string {
	return filepath.Join(s.dir, "buckets", bucket, ".meta", filepath.FromSlash(key))
}

func (s *BucketStore) Get(ctx context.Context, bucket, key string) (backends.BlobData, error) {
	path, err := s.objectPath(bucket, key)
	if err != nil {
		return backends.BlobData{}, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return backends.BlobData{}, backends.ErrNotFound
	}
	if err != nil {
		return backends.BlobData{}, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return backends.BlobData{}, err
	}
	ct, _ := os.ReadFile(s.metaPath(bucket, key))
	return backends.BlobData{Reader: f, ContentType: string(ct), Size: info.Size()}, nil
}

func (s *BucketStore) Put(ctx context.Context, bucket, key string, data backends.BlobData) error {
	path, err := s.objectPath(bucket, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	written, err := io.Copy(f, data.Reader)
	if err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	meta := s.metaPath(bucket, key)
	if err := os.MkdirAll(filepath.Dir(meta), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(meta, []byte(data.ContentType), 0o644); err != nil {
		return err
	}
	s.mu.Lock()
	observers := append([]func(string, backends.ObjectMeta){}, s.onPut...)
	s.mu.Unlock()
	for _, fn := range observers {
		fn(bucket, backends.ObjectMeta{
			Key:         key,
			Size:        written,
			ContentType: data.ContentType,
			Updated:     time.Now().UTC(),
		})
	}
	return nil
}

func (s *BucketStore) Delete(ctx context.Context, bucket, key string) error {
	path, err := s.objectPath(bucket, key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(s.metaPath(bucket, key)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *BucketStore) Exists(ctx context.Context, bucket, key string) (bool, error) {
	path, err := s.objectPath(bucket, key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (s *BucketStore) List(ctx context.Context, bucket, prefix string) ([]backends.ObjectMeta, error) {
	root := filepath.Join(s.dir, "buckets", bucket)
	var out []backends.ObjectMeta
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == root {
				return filepath.SkipAll // bucket never written: empty list
			}
			return err
		}
		if d.IsDir() {
			if d.Name() == ".meta" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		ct, _ := os.ReadFile(s.metaPath(bucket, key))
		out = append(out, backends.ObjectMeta{
			Key:         key,
			Size:        info.Size(),
			ContentType: string(ct),
			Updated:     info.ModTime().UTC(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
