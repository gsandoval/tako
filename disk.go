package tako

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/gsandoval/tako/internal/hooks"
)

// Disk declares a remote filesystem volume that is automatically mounted
// into any Cloud Run service or job whose handlers reference it.
//
// By default a Disk is persistent and shared across instances (backed by a
// Cloud Storage bucket mounted via Cloud Storage FUSE in Cloud Run, and by
// .tako/data/disks/<name>/ during tako dev and tests). Setting
// DiskConfig{Ephemeral: true} provisions a fast in-memory scratch volume
// (emptyDir) per instance instead.
//
//	var Scratch = tako.Disk("scratch", tako.DiskConfig{
//		Ephemeral: true,
//		SizeLimit: "1Gi",
//	})
func Disk(name string, cfg ...DiskConfig) *DiskHandle {
	var c DiskConfig
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return &DiskHandle{name: name, cfg: c}
}

// DiskHandle is a handle to a declared Disk resource. Every file operation
// takes a context.Context so Tako's static analyzer can trace handler edges
// and so NewTestApp(t) can isolate disk directories per test.
type DiskHandle struct {
	name string
	cfg  DiskConfig
}

// Name returns the logical name of the disk as declared in tako.Disk.
func (d *DiskHandle) Name() string { return d.name }

var errDiskTraversal = errors.New("tako: disk path escapes mount root")
var errDiskReadOnly = errors.New("tako: write to read-only disk")

// root returns the host filesystem root directory for this disk under ctx.
func (d *DiskHandle) root(ctx context.Context) string {
	dir := ""
	if a := appFrom(ctx); a != nil && a.Buckets != nil {
		dir = extractBucketDir(a.Buckets)
	} else if Environment() == EnvCloud {
		if root := os.Getenv("TAKO_DISK_ROOT"); root != "" {
			return filepath.Join(root, d.name)
		}
		return filepath.Join("/mnt/tako", d.name)
	} else if hooks.Bucket != nil {
		dir = extractBucketDir(hooks.Bucket)
		if dir == "" {
			dir = ".tako/data"
		}
	} else {
		panic(unwiredPanic("disk"))
	}

	subdir := "disks"
	if d.cfg.Ephemeral {
		subdir = "disks-ephemeral"
	}
	base := filepath.Join(dir, subdir, d.name)
	_ = os.MkdirAll(base, 0o755)
	return base
}

func extractBucketDir(b any) string {
	v := reflect.ValueOf(b)
	if v.Kind() == reflect.Ptr && !v.IsNil() {
		v = v.Elem()
	}
	if v.Kind() == reflect.Struct {
		f := v.FieldByName("dir")
		if f.IsValid() && f.Kind() == reflect.String {
			return f.String()
		}
	}
	return ""
}

// resolve validates and joins rel against the disk's root directory,
// rejecting absolute paths and any ".." traversal that escapes the mount.
func (d *DiskHandle) resolve(ctx context.Context, rel string) (string, error) {
	base := d.root(ctx)
	clean := filepath.Clean(rel)
	if clean == "." {
		return base, nil
	}
	if filepath.IsAbs(rel) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errDiskTraversal
	}
	full := filepath.Join(base, clean)
	relCheck, err := filepath.Rel(base, full)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", errDiskTraversal
	}
	return full, nil
}

// Path returns the resolved absolute filesystem path for rel within the
// mounted disk, so callers can pass it to external binaries (such as ffmpeg
// or sqlite3) or standard library helpers. Path panics if rel attempts to
// escape the disk root via "..".
func (d *DiskHandle) Path(ctx context.Context, rel string) string {
	p, err := d.resolve(ctx, rel)
	if err != nil {
		panic(err)
	}
	return p
}

// ReadFile reads the named file relative to the disk root and returns its
// contents.
func (d *DiskHandle) ReadFile(ctx context.Context, name string) ([]byte, error) {
	p, err := d.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// WriteFile writes data to the named file relative to the disk root,
// creating parent directories as needed.
func (d *DiskHandle) WriteFile(ctx context.Context, name string, data []byte, perm fs.FileMode) error {
	if d.cfg.ReadOnly {
		return errDiskReadOnly
	}
	p, err := d.resolve(ctx, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, perm)
}

// Open opens the named file relative to the disk root for reading.
func (d *DiskHandle) Open(ctx context.Context, name string) (*os.File, error) {
	p, err := d.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// Create creates or truncates the named file relative to the disk root,
// creating parent directories as needed.
func (d *DiskHandle) Create(ctx context.Context, name string) (*os.File, error) {
	if d.cfg.ReadOnly {
		return nil, errDiskReadOnly
	}
	p, err := d.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	return os.Create(p)
}

// OpenFile is the generalized open call relative to the disk root, creating
// parent directories when O_CREATE is set.
func (d *DiskHandle) OpenFile(ctx context.Context, name string, flag int, perm fs.FileMode) (*os.File, error) {
	if d.cfg.ReadOnly && (flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0) {
		return nil, errDiskReadOnly
	}
	p, err := d.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if flag&os.O_CREATE != 0 {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(p, flag, perm)
}

// Remove removes the named file or empty directory relative to the disk root.
func (d *DiskHandle) Remove(ctx context.Context, name string) error {
	if d.cfg.ReadOnly {
		return errDiskReadOnly
	}
	p, err := d.resolve(ctx, name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// Stat returns a FileInfo describing the named file relative to the disk root.
func (d *DiskHandle) Stat(ctx context.Context, name string) (fs.FileInfo, error) {
	p, err := d.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	return os.Stat(p)
}

// MkdirAll creates a directory named path relative to the disk root, along
// with any necessary parents.
func (d *DiskHandle) MkdirAll(ctx context.Context, path string, perm fs.FileMode) error {
	if d.cfg.ReadOnly {
		return errDiskReadOnly
	}
	p, err := d.resolve(ctx, path)
	if err != nil {
		return err
	}
	return os.MkdirAll(p, perm)
}

// FS returns an fs.FS rooted at the disk's mount directory.
func (d *DiskHandle) FS(ctx context.Context) fs.FS {
	return os.DirFS(d.root(ctx))
}
