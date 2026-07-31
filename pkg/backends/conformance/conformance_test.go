package conformance

import (
	"flag"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
	"github.com/gsandoval/tako/pkg/backends/local"
)

var backend = flag.String("backend", "local", "backend to run the conformance suite against (local|gcp)")

func TestEntity(t *testing.T) {
	switch *backend {
	case "local":
		RunEntity(t, func(t *testing.T) backends.Entity {
			s, err := local.OpenEntity(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			return s
		})
	case "gcp":
		// Each chapter gets a throwaway Datastore namespace
		// (conf-<hex>) in the test project. Namespaces are not deleted
		// afterwards — they hold a handful of tiny entities in the dev
		// sandbox project; purge them with `tako destroy` era tooling
		// or the console if they bother you.
		RunEntity(t, func(t *testing.T) backends.Entity {
			c := gcpClient(t, "conf", "conf-"+randHex(t))
			ensureAndIndex(t, c)
			return gcpb.NewEntityStore(c)
		})
	default:
		t.Fatalf("unknown -backend %q", *backend)
	}
}

func TestBucket(t *testing.T) {
	switch *backend {
	case "local":
		RunBucket(t, func(t *testing.T) backends.Bucket {
			return local.NewBucketStore(t.TempDir())
		})
	case "gcp":
		// Chapters use logical buckets "photos" and "thumbs"; a random
		// app scope isolates each chapter's physical buckets, which are
		// created here and deleted (objects first) on cleanup.
		RunBucket(t, func(t *testing.T) backends.Bucket {
			c := gcpClient(t, "conf"+randHex(t), "prod")
			for _, logical := range []string{"photos", "thumbs"} {
				makeBucket(t, c, gcpb.BucketName(*project, c.Opts.App, "prod", logical))
			}
			return gcpb.NewBucketStore(c)
		})
	default:
		t.Fatalf("unknown -backend %q", *backend)
	}
}

func TestQueue(t *testing.T) {
	switch *backend {
	case "local":
		RunQueue(t, func(t *testing.T) backends.Queue {
			return local.NewQueueDispatcher(t.TempDir(), local.QueueOptions{
				MaxAttempts: 3,
				Backoff:     10 * time.Millisecond,
			})
		}, QueueParams{MaxAttempts: 3})
	case "gcp":
		t.Skip("delivery on gcp is Pub/Sub push into Cloud Run (infrastructure, validated by the gate scripts); publishing runs live in the gate cycle")
	default:
		t.Fatalf("unknown -backend %q", *backend)
	}
}

func TestQueueDelay(t *testing.T) {
	switch *backend {
	case "local":
		RunQueueDelay(t, func(t *testing.T) backends.Queue {
			return local.NewQueueDispatcher(t.TempDir(), local.QueueOptions{
				MaxAttempts: 3,
				Backoff:     10 * time.Millisecond,
			})
		})
	case "gcp":
		t.Skip("WithDelay on gcp waits for the delayed-delivery feature (Cloud Tasks); Publish errors with that pointer, pinned in the gcp package")
	default:
		t.Fatalf("unknown -backend %q", *backend)
	}
}

func TestCache(t *testing.T) {
	switch *backend {
	case "local":
		RunCache(t, func(t *testing.T) backends.Cache {
			return local.NewMemoryCache()
		})
	case "gcp":
		t.Skip("cloud cache is per-instance in-memory too — same backend, Phase E wires it")
	default:
		t.Fatalf("unknown -backend %q", *backend)
	}
}

func TestTopic(t *testing.T) {
	switch *backend {
	case "local":
		RunTopic(t, func(t *testing.T) backends.Topic {
			return local.NewQueueDispatcher(t.TempDir(), local.QueueOptions{
				MaxAttempts: 3,
				Backoff:     10 * time.Millisecond,
			}).Topics()
		}, TopicParams{MaxAttempts: 3})
	case "gcp":
		t.Skip("delivery on gcp is Pub/Sub push into Cloud Run (infrastructure, validated by the gate scripts); publishing + fan-out run in TestTopicGCP")
	default:
		t.Fatalf("unknown -backend %q", *backend)
	}
}

func TestSignedURL(t *testing.T) {
	switch *backend {
	case "local":
		RunSignedURL(t, func(t *testing.T) SignedURLEnv {
			dir := t.TempDir()
			store := local.NewBucketStore(dir)
			d := local.NewQueueDispatcher(dir, local.QueueOptions{
				MaxAttempts: 3,
				Backoff:     10 * time.Millisecond,
			})
			local.WireBucketEvents(store, d)
			signer, err := local.NewURLSigner(dir, "http://placeholder")
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(local.SignedURLEndpoint(signer, store))
			t.Cleanup(srv.Close)
			// Re-mint with the real server address as the base.
			signer, err = local.NewURLSigner(dir, srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			return SignedURLEnv{Signer: signer, Client: srv.Client(), Bucket: store, Topic: d.Topics()}
		}, SignedURLParams{UploadEvents: true, WildcardAtUpload: true})
	case "gcp":
		// GCS enforces identity, expiry, exact signed content types, and
		// (T1) MaxSize via the signed size-range header; WILDCARD types
		// remain the platform gap, pinned by WildcardAtUpload=false.
		RunSignedURL(t, gcpSignedEnv, SignedURLParams{})
	default:
		t.Fatalf("unknown -backend %q", *backend)
	}
}

func TestBucketEvents(t *testing.T) {
	switch *backend {
	case "local":
		RunBucketEvents(t, func(t *testing.T) (backends.Bucket, backends.Topic) {
			dir := t.TempDir()
			store := local.NewBucketStore(dir)
			d := local.NewQueueDispatcher(dir, local.QueueOptions{
				MaxAttempts: 3,
				Backoff:     10 * time.Millisecond,
			})
			local.WireBucketEvents(store, d)
			return store, d.Topics()
		})
	case "gcp":
		t.Skip("bucket events on gcp are GCS-native notifications; the config + payload path runs in TestBucketNotificationGCP")
	default:
		t.Fatalf("unknown -backend %q", *backend)
	}
}

func TestLease(t *testing.T) {
	switch *backend {
	case "local":
		RunLease(t, func(t *testing.T) (backends.Lease, func(name string)) {
			l := local.NewMemoryLease()
			return l, l.Break
		})
	case "gcp":
		// Leases are objects in the SHARED framework bucket
		// (<project>-tako) — ensured here, never deleted (deploys and
		// other apps use it); each chapter's objects are scoped by a
		// random app name and removed on cleanup. The breaker is an
		// unconditional object delete — exactly what an operator does
		// to force-release a wedged lease.
		RunLease(t, func(t *testing.T) (backends.Lease, func(name string)) {
			c := gcpClient(t, "conf"+randHex(t), "prod")
			ensureFrameworkBucket(t, c)
			s := gcpb.NewLeaseStore(c)
			t.Cleanup(func() { purgeLeaseObjects(t, c) })
			return s, func(name string) { breakLease(t, c, name) }
		})
	default:
		t.Fatalf("unknown -backend %q", *backend)
	}
}
