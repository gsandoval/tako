package conformance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
)

// -project selects the GCP test project for -backend=gcp runs. The suite
// refuses to run without it — conformance against a real project must
// never be accidental.
var project = flag.String("project", "", "GCP project for -backend=gcp (required; use the dedicated test project)")

func gcpClient(t *testing.T, app, env string) *gcpb.Client {
	t.Helper()
	if *project == "" {
		t.Fatal("-backend=gcp needs -project <test-project-id> — refusing to guess a project")
	}
	c, err := gcpb.New(context.Background(), gcpb.Options{Project: *project, App: app, Env: env})
	if err != nil {
		t.Fatalf("gcp auth: %v", err)
	}
	return c
}

func randHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// makeBucket creates a physical GCS bucket for one conformance chapter
// and registers deletion (objects first) as cleanup.
func makeBucket(t *testing.T, c *gcpb.Client, name string) {
	t.Helper()
	ctx := context.Background()
	body := strings.NewReader(fmt.Sprintf(`{"name":%q,"iamConfiguration":{"uniformBucketLevelAccess":{"enabled":true}}}`, name))
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://storage.googleapis.com/storage/v1/b?project="+*project, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HC.Do(req)
	if err != nil {
		t.Fatalf("create bucket %s: %v", name, err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("create bucket %s: HTTP %d", name, resp.StatusCode)
	}
	t.Cleanup(func() { deleteBucket(t, c, name) })
}

func deleteBucket(t *testing.T, c *gcpb.Client, name string) {
	ctx := context.Background()
	// Objects first: GCS refuses to delete non-empty buckets.
	listURL := "https://storage.googleapis.com/storage/v1/b/" + name + "/o?fields=items(name)"
	resp, err := c.HC.Get(listURL)
	if err == nil {
		var page struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
		}
		_ = jsonDecode(resp, &page)
		for _, it := range page.Items {
			req, _ := http.NewRequestWithContext(ctx, "DELETE",
				"https://storage.googleapis.com/storage/v1/b/"+name+"/o/"+escapePath(it.Name), nil)
			if r, err := c.HC.Do(req); err == nil {
				r.Body.Close()
			}
		}
	}
	req, _ := http.NewRequestWithContext(ctx, "DELETE", "https://storage.googleapis.com/storage/v1/b/"+name, nil)
	if r, err := c.HC.Do(req); err == nil {
		r.Body.Close()
	}
}

func jsonDecode(resp *http.Response, v any) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func escapePath(s string) string { return url.PathEscape(s) }

// ensureAndIndex creates the composite index the AND chapter needs
// (owner asc, hits asc — equality + range across two fields), waiting
// until it serves. Deploys derive exactly this from the app graph; the
// conformance database has no deploy, so the suite provisions its own.
var andIndexOnce sync.Once

func ensureAndIndex(t *testing.T, c *gcpb.Client) {
	t.Helper()
	andIndexOnce.Do(func() {
		ctx := context.Background()
		base := "https://datastore.googleapis.com/v1/projects/" + *project + "/indexes"
		body := strings.NewReader(`{"kind":"links","ancestor":"NONE","properties":[{"name":"owner","direction":"ASCENDING"},{"name":"hits","direction":"ASCENDING"}]}`)
		req, err := http.NewRequestWithContext(ctx, "POST", base, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HC.Do(req)
		if err != nil {
			t.Fatalf("create conformance index: %v", err)
		}
		resp.Body.Close()
		// 409 = already there from an earlier run; anything else fatal.
		if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusConflict {
			t.Fatalf("create conformance index: HTTP %d", resp.StatusCode)
		}
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			r, err := c.HC.Get(base)
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Indexes []struct {
					Kind       string `json:"kind"`
					State      string `json:"state"`
					Properties []struct {
						Name string `json:"name"`
					} `json:"properties"`
				} `json:"indexes"`
			}
			err = jsonDecode(r, &out)
			if err != nil {
				t.Fatal(err)
			}
			for _, ix := range out.Indexes {
				if ix.Kind == "links" && len(ix.Properties) == 2 &&
					ix.Properties[0].Name == "owner" && ix.Properties[1].Name == "hits" &&
					ix.State == "READY" {
					return
				}
			}
			time.Sleep(5 * time.Second)
		}
		t.Fatal("conformance index did not become READY within 3m")
	})
}

// --- C4: Pub/Sub-backed chapters ---------------------------------------
//
// Push DELIVERY is infrastructure (subscription → Cloud Run → IAM) and
// is validated by the live gate scripts; what the backend owns is
// publishing. These chapters prove it against real Pub/Sub with
// harness pull subscriptions: fan-out (two subscriptions each see every
// message) and bucket-event emission (a Put announces ObjectMeta on the
// bucket's events topic).

func makeTopic(t *testing.T, c *gcpb.Client, name string) {
	t.Helper()
	ctx := context.Background()
	full := "https://pubsub.googleapis.com/v1/projects/" + *project + "/topics/" + name
	req, err := http.NewRequestWithContext(ctx, "PUT", full, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HC.Do(req)
	if err != nil {
		t.Fatalf("create topic %s: %v", name, err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("create topic %s: HTTP %d", name, resp.StatusCode)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequestWithContext(context.Background(), "DELETE", full, nil)
		if r, err := c.HC.Do(req); err == nil {
			r.Body.Close()
		}
	})
}

func makePullSub(t *testing.T, c *gcpb.Client, sub, topic string) {
	t.Helper()
	ctx := context.Background()
	full := "https://pubsub.googleapis.com/v1/projects/" + *project + "/subscriptions/" + sub
	body := strings.NewReader(fmt.Sprintf(`{"topic":"projects/%s/topics/%s"}`, *project, topic))
	req, err := http.NewRequestWithContext(ctx, "PUT", full, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HC.Do(req)
	if err != nil {
		t.Fatalf("create subscription %s: %v", sub, err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("create subscription %s: HTTP %d", sub, resp.StatusCode)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequestWithContext(context.Background(), "DELETE", full, nil)
		if r, err := c.HC.Do(req); err == nil {
			r.Body.Close()
		}
	})
}

// pullPayloads pulls (and acks) until want messages arrive or the
// deadline passes, returning the decoded payloads.
func pullPayloads(t *testing.T, c *gcpb.Client, sub string, want int) [][]byte {
	t.Helper()
	ctx := context.Background()
	u := "https://pubsub.googleapis.com/v1/projects/" + *project + "/subscriptions/" + sub
	var got [][]byte
	deadline := time.Now().Add(45 * time.Second)
	for len(got) < want && time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, "POST", u+":pull", strings.NewReader(`{"maxMessages":10}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HC.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			ReceivedMessages []struct {
				AckID   string `json:"ackId"`
				Message struct {
					Data []byte `json:"data"`
				} `json:"message"`
			} `json:"receivedMessages"`
		}
		if err := jsonDecode(resp, &out); err != nil {
			t.Fatal(err)
		}
		var acks []string
		for _, m := range out.ReceivedMessages {
			got = append(got, m.Message.Data)
			acks = append(acks, fmt.Sprintf("%q", m.AckID))
		}
		if len(acks) > 0 {
			body := strings.NewReader(`{"ackIds":[` + strings.Join(acks, ",") + `]}`)
			req, _ := http.NewRequestWithContext(ctx, "POST", u+":acknowledge", body)
			req.Header.Set("Content-Type", "application/json")
			if r, err := c.HC.Do(req); err == nil {
				r.Body.Close()
			}
		}
	}
	if len(got) < want {
		t.Fatalf("subscription %s delivered %d message(s), want %d", sub, len(got), want)
	}
	return got
}

func TestTopicGCP(t *testing.T) {
	if *backend != "gcp" {
		t.Skip("gcp-only chapter (-backend=gcp)")
	}
	env := "conf-" + randHex(t)
	c := gcpClient(t, "conf", env)
	topic := gcpb.TopicName(env, "fanout")
	makeTopic(t, c, topic)
	makePullSub(t, c, topic+"-a", topic)
	makePullSub(t, c, topic+"-b", topic)

	store := gcpb.NewTopicStore(c)
	for _, payload := range []string{`{"n":1}`, `{"n":2}`} {
		if err := store.Publish(context.Background(), "fanout", []byte(payload)); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	// Fan-out: BOTH subscriptions see every message.
	for _, sub := range []string{topic + "-a", topic + "-b"} {
		got := pullPayloads(t, c, sub, 2)
		seen := map[string]bool{}
		for _, p := range got {
			seen[string(p)] = true
		}
		if !seen[`{"n":1}`] || !seen[`{"n":2}`] {
			t.Fatalf("subscription %s saw %q, want both messages", sub, got)
		}
	}
}

func TestBucketEventEmitGCP(t *testing.T) {
	if *backend != "gcp" {
		t.Skip("gcp-only chapter (-backend=gcp)")
	}
	env := "conf-" + randHex(t)
	c := gcpClient(t, "conf", env)
	c.Opts.BucketEvents = map[string]bool{"evbkt": true}
	topic := gcpb.EventTopicName(env, "evbkt")
	makeTopic(t, c, topic)
	makePullSub(t, c, topic+"-keep", topic)
	physical := gcpb.BucketName(*project, "conf", env, "evbkt")
	makeBucket(t, c, physical)

	store := gcpb.NewBucketStore(c)
	if err := store.Put(context.Background(), "evbkt", "hello.txt",
		backends.BlobData{Reader: strings.NewReader("hi there"), ContentType: "text/plain", Size: 8}); err != nil {
		t.Fatalf("put: %v", err)
	}
	got := pullPayloads(t, c, topic+"-keep", 1)
	var meta struct {
		Key         string `json:"key"`
		Size        int64  `json:"size"`
		ContentType string `json:"contentType"`
	}
	if err := json.Unmarshal(got[0], &meta); err != nil {
		t.Fatalf("event payload %q: %v", got[0], err)
	}
	if meta.Key != "hello.txt" || meta.Size != 8 || meta.ContentType != "text/plain" {
		t.Fatalf("event meta = %+v", meta)
	}
}
