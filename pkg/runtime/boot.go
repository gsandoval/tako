package runtime

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends/local"
)

var (
	bootedStore   io.Closer
	bootedQueue   *local.QueueDispatcher
	bootedBuckets *local.BucketStore
	bootedSigner  *local.URLSigner
)

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
	buckets := local.NewBucketStore(dir)
	hooks.Bucket = buckets
	dispatcher := local.NewQueueDispatcher(dir, local.QueueOptions{})
	attachQueueConsole(dispatcher, dir)
	hooks.Queue = dispatcher
	hooks.Topic = dispatcher.Topics()
	// Every bucket write publishes on "<bucket>.Uploaded" — the local
	// stand-in for GCS notifications.
	local.WireBucketEvents(buckets, dispatcher)
	// Signed URLs point back at this service's own /_tako/signed/
	// endpoint; the signing key persists under dir so URLs survive
	// reloads.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	signer, err := local.NewURLSigner(dir, "http://localhost:"+port)
	if err != nil {
		return err
	}
	hooks.Signer = signer
	bootedSigner = signer
	bootedBuckets = buckets
	bootedQueue = dispatcher
	bootedStore = store
	return nil
}

// SignedURLHandler serves the local signed-URL endpoint
// (/_tako/signed/<token>) against the booted backends. Generated mains
// mount it when the app declares a bucket.
func SignedURLHandler() http.Handler {
	if bootedSigner == nil || bootedBuckets == nil {
		panic("tako: SignedURLHandler before Boot")
	}
	return local.SignedURLEndpoint(bootedSigner, bootedBuckets)
}

// WaitIdle blocks until local queues drain (every published message has
// acked or dead-lettered). Test plumbing until tako.NewTestApp's
// DrainQueues lands in F7.
func WaitIdle(timeout time.Duration) error {
	if bootedQueue == nil {
		return nil
	}
	return bootedQueue.WaitIdle(timeout)
}
