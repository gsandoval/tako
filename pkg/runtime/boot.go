package runtime

import (
	"fmt"
	"io"
	"os"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends/local"
)

var bootedStore io.Closer

// Boot wires the service's backends per TAKO_BACKEND (default "local":
// in-process backends under .tako/data). Generated mains call it first.
func Boot() {
	switch backend := os.Getenv("TAKO_BACKEND"); backend {
	case "", "local":
		if err := BootLocal(".tako/data"); err != nil {
			fmt.Fprintf(os.Stderr, "tako: boot: %v\n", err)
			os.Exit(1)
		}
	case "gcp":
		fmt.Fprintln(os.Stderr, "tako: boot: the gcp backend arrives in Phase E")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "tako: boot: unknown TAKO_BACKEND %q\n", backend)
		os.Exit(1)
	}
}

// BootLocal wires the in-process backends rooted at dir. Tests use it
// directly with a temp dir until tako.NewTestApp lands (F7); calling it
// again closes the previously booted store first.
func BootLocal(dir string) error {
	if bootedStore != nil {
		_ = bootedStore.Close()
		bootedStore = nil
	}
	store, err := local.OpenEntity(dir)
	if err != nil {
		return err
	}
	hooks.Entity = store
	hooks.Bucket = local.NewBucketStore(dir)
	bootedStore = store
	return nil
}
