package conformance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// RunBucketEvents runs the bucket-event contract against wired pairs from
// open: every successful Put on the bucket publishes the object's
// ObjectMeta on the topic "<bucket>.Uploaded". Locally the wiring is
// local.WireBucketEvents; in the cloud it is a GCS notification (Phase F)
// — this suite is the shared contract.
func RunBucketEvents(t *testing.T, open func(t *testing.T) (backends.Bucket, backends.Topic)) {
	ctx := context.Background()
	const wait = 10 * time.Second

	t.Run("put publishes the object meta on <bucket>.Uploaded", func(t *testing.T) {
		bucket, topic := open(t)
		got := make(chan []byte, 1)
		topic.Subscribe("photos.Uploaded", "OnUploaded", func(ctx context.Context, p []byte) error {
			got <- p
			return nil
		})
		blob := backends.BlobData{Reader: strings.NewReader("JPEGBYTES"), ContentType: "image/jpeg", Size: 9}
		if err := bucket.Put(ctx, "photos", "cat", blob); err != nil {
			t.Fatal(err)
		}
		select {
		case p := <-got:
			var meta backends.ObjectMeta
			if err := json.Unmarshal(p, &meta); err != nil {
				t.Fatalf("payload does not decode as ObjectMeta: %v (%s)", err, p)
			}
			if meta.Key != "cat" || meta.Size != 9 || meta.ContentType != "image/jpeg" || meta.Updated.IsZero() {
				t.Fatalf("meta = %+v", meta)
			}
		case <-time.After(wait):
			t.Fatal("no upload event arrived")
		}
	})

	t.Run("every write fires, including a consumer's own", func(t *testing.T) {
		bucket, topic := open(t)
		keys := make(chan string, 4)
		// The subscriber writes a derived object on the first event — its
		// own write must fire too (consumers filter their output by key).
		topic.Subscribe("photos.Uploaded", "OnUploaded", func(ctx context.Context, p []byte) error {
			var meta backends.ObjectMeta
			if err := json.Unmarshal(p, &meta); err != nil {
				return err
			}
			keys <- meta.Key
			if !strings.HasSuffix(meta.Key, "_thumb") {
				return bucket.Put(ctx, "photos", meta.Key+"_thumb",
					backends.BlobData{Reader: strings.NewReader("t"), ContentType: "image/jpeg", Size: 1})
			}
			return nil
		})
		if err := bucket.Put(ctx, "photos", "dog",
			backends.BlobData{Reader: strings.NewReader("d"), ContentType: "image/jpeg", Size: 1}); err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{"dog": false, "dog_thumb": false}
		for range 2 {
			select {
			case k := <-keys:
				want[k] = true
			case <-time.After(wait):
				t.Fatalf("missing events; saw %v", want)
			}
		}
		if !want["dog"] || !want["dog_thumb"] {
			t.Fatalf("events = %v", want)
		}
		select {
		case k := <-keys:
			t.Fatalf("unexpected third event for %q", k)
		case <-time.After(300 * time.Millisecond):
		}
	})

	t.Run("buckets fire on their own topics", func(t *testing.T) {
		bucket, topic := open(t)
		photos := make(chan string, 1)
		topic.Subscribe("docs.Uploaded", "OnDoc", func(ctx context.Context, p []byte) error {
			photos <- string(p)
			return nil
		})
		if err := bucket.Put(ctx, "photos", "cat",
			backends.BlobData{Reader: strings.NewReader("c"), ContentType: "image/jpeg", Size: 1}); err != nil {
			t.Fatal(err)
		}
		select {
		case p := <-photos:
			t.Fatalf("docs subscriber received photos event: %s", p)
		case <-time.After(300 * time.Millisecond):
		}
	})
}
