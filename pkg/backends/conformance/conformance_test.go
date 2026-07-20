package conformance

import (
	"flag"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
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
		t.Skip("gcp backend arrives in Phase E")
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
		t.Skip("gcp backend arrives in Phase E")
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
		t.Skip("gcp backend arrives in Phase F")
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
		t.Skip("gcp backend arrives in Phase F")
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
		t.Skip("gcp backend arrives in Phase F")
	default:
		t.Fatalf("unknown -backend %q", *backend)
	}
}
