package tako_test

import (
	"context"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/gsandoval/tako"
)

var (
	testScratchDisk = tako.Disk("scratch", tako.DiskConfig{
		Ephemeral: true,
		SizeLimit: "512Mi",
	})
	testWeightsDisk = tako.Disk("weights", tako.DiskConfig{
		ReadOnly: true,
	})
	testSharedDisk = tako.Disk("shared")
)

func TestDiskReadWriteAndIsolation(t *testing.T) {
	app1 := tako.NewTestApp(t)
	app2 := tako.NewTestApp(t)
	ctx1 := app1.Ctx()
	ctx2 := app2.Ctx()

	if got := testSharedDisk.Name(); got != "shared" {
		t.Fatalf("Name() = %q, want shared", got)
	}

	if err := testSharedDisk.WriteFile(ctx1, "nested/hello.txt", []byte("world"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := testSharedDisk.ReadFile(ctx1, "nested/hello.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "world" {
		t.Fatalf("ReadFile = %q, want world", string(data))
	}

	// Second TestApp is isolated.
	if _, err := testSharedDisk.Stat(ctx2, "nested/hello.txt"); !os.IsNotExist(err) {
		t.Fatalf("expected file not to exist in app2, got err=%v", err)
	}

	// Create, Open, OpenFile, Stat, MkdirAll, FS, Remove.
	if err := testScratchDisk.MkdirAll(ctx1, "work/sub", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	f, err := testScratchDisk.Create(ctx1, "work/sub/item.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, _ = f.WriteString("abc")
	_ = f.Close()

	af, err := testScratchDisk.OpenFile(ctx1, "work/sub/item.txt", os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	_, _ = af.WriteString("def")
	_ = af.Close()

	rf, err := testScratchDisk.Open(ctx1, "work/sub/item.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	all, err := io.ReadAll(rf)
	_ = rf.Close()
	if err != nil || string(all) != "abcdef" {
		t.Fatalf("Open/ReadAll = %q, err=%v; want abcdef", string(all), err)
	}

	info, err := testScratchDisk.Stat(ctx1, "work/sub/item.txt")
	if err != nil || info.Size() != 6 {
		t.Fatalf("Stat size = %v, err=%v; want 6", info, err)
	}

	fsBytes, err := fs.ReadFile(testScratchDisk.FS(ctx1), "work/sub/item.txt")
	if err != nil || string(fsBytes) != "abcdef" {
		t.Fatalf("FS ReadFile = %q, err=%v", string(fsBytes), err)
	}

	p := testScratchDisk.Path(ctx1, "work/sub/item.txt")
	if !strings.HasSuffix(p, "work/sub/item.txt") {
		t.Fatalf("Path = %q, expected suffix work/sub/item.txt", p)
	}

	if err := testScratchDisk.Remove(ctx1, "work/sub/item.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := testScratchDisk.Stat(ctx1, "work/sub/item.txt"); !os.IsNotExist(err) {
		t.Fatalf("after Remove, Stat err = %v, want IsNotExist", err)
	}
}

func TestDiskPathTraversalRejected(t *testing.T) {
	app := tako.NewTestApp(t)
	ctx := app.Ctx()

	for _, bad := range []string{"../secret.txt", "a/../../secret.txt", "/etc/passwd"} {
		if _, err := testSharedDisk.ReadFile(ctx, bad); err == nil {
			t.Errorf("ReadFile(%q) succeeded, expected traversal error", bad)
		}
		if err := testSharedDisk.WriteFile(ctx, bad, []byte("x"), 0o644); err == nil {
			t.Errorf("WriteFile(%q) succeeded, expected traversal error", bad)
		}
	}

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Path(\"../escape\") did not panic")
		}
	}()
	_ = testSharedDisk.Path(ctx, "../escape")
}

func TestDiskReadOnlyRejectsWrites(t *testing.T) {
	app := tako.NewTestApp(t)
	ctx := app.Ctx()

	if err := testWeightsDisk.WriteFile(ctx, "model.bin", []byte("x"), 0o644); err == nil {
		t.Errorf("WriteFile on ReadOnly disk succeeded, expected error")
	}
	if _, err := testWeightsDisk.Create(ctx, "model.bin"); err == nil {
		t.Errorf("Create on ReadOnly disk succeeded, expected error")
	}
	if _, err := testWeightsDisk.OpenFile(ctx, "model.bin", os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		t.Errorf("OpenFile(O_WRONLY) on ReadOnly disk succeeded, expected error")
	}
	if err := testWeightsDisk.Remove(ctx, "model.bin"); err == nil {
		t.Errorf("Remove on ReadOnly disk succeeded, expected error")
	}
	if err := testWeightsDisk.MkdirAll(ctx, "sub", 0o755); err == nil {
		t.Errorf("MkdirAll on ReadOnly disk succeeded, expected error")
	}
}

func TestDiskCloudMountPath(t *testing.T) {
	mountRoot := t.TempDir()
	t.Setenv("TAKO_BACKEND", "gcp")
	t.Setenv("TAKO_DISK_ROOT", mountRoot)

	ctx := context.Background()
	if err := testSharedDisk.WriteFile(ctx, "cloud/data.txt", []byte("from-fuse"), 0o644); err != nil {
		t.Fatalf("WriteFile in cloud mode: %v", err)
	}
	got, err := testSharedDisk.ReadFile(ctx, "cloud/data.txt")
	if err != nil || string(got) != "from-fuse" {
		t.Fatalf("ReadFile in cloud mode = %q, err=%v; want from-fuse", string(got), err)
	}
}

