package docs_test

// api/surface.txt is a copy of testdata/sdk_lock.golden, and this test
// is the whole reason the copy is allowed to exist.
//
// The api section of `tako docs` lists every exported symbol with its
// signature. That list has to come from somewhere the binary can read
// without source, and the lock — which the API-freeze test already
// maintains — is the one place the surface is written down. Embedding
// needs the file inside docs/, so the file is duplicated and pinned
// byte-for-byte rather than re-derived: two renderings of "the exported
// surface" could disagree, one copy under equality cannot.
//
// When an API change lands, sdk_lock.golden is regenerated deliberately
// (its own task, its own review) and this test fails until the copy
// follows:
//
//	go test ./docs -update

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/api/surface.txt from testdata/sdk_lock.golden")

func TestEmbeddedSurfaceMatchesTheSDKLock(t *testing.T) {
	lockPath := filepath.Join("..", "testdata", "sdk_lock.golden")
	lock, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("reading the SDK lock: %v", err)
	}

	copyPath := filepath.Join("api", "surface.txt")
	if *update {
		if err := os.WriteFile(copyPath, lock, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatalf("reading %s (run with -update to create it): %v", copyPath, err)
	}
	if string(got) != string(lock) {
		t.Fatalf("%s is not the SDK lock.\n"+
			"The API changed and the docs did not follow — regenerate with: go test ./docs -update",
			copyPath)
	}
}
