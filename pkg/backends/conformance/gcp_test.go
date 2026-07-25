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
	body := strings.NewReader(fmt.Sprintf(`{"name":%q,"iamConfiguration":{"uniformBucketLevelAccess":{"enabled":true}},"cors":[{"origin":["*"],"method":["GET","PUT","HEAD"],"responseHeader":["Content-Type"],"maxAgeSeconds":3600}]}`, name))
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

// --- C5: signed URLs + GCS notifications --------------------------------

// signerSA is the conformance signing identity: the harness cannot sign
// as a Cloud Run service (there is none), so an idempotently-created SA
// stands in — the ADC principal signs blobs as it (project owners can;
// otherwise the fatal prints the grant). It persists across runs, like
// the conformance composite index.
const signerSA = "conf-signer"

var signerOnce sync.Once

func ensureSignerSA(t *testing.T, c *gcpb.Client) string {
	t.Helper()
	email := signerSA + "@" + *project + ".iam.gserviceaccount.com"
	signerOnce.Do(func() {
		ctx := context.Background()
		body := strings.NewReader(fmt.Sprintf(`{"accountId":%q,"serviceAccount":{"displayName":"tako conformance signer"}}`, signerSA))
		req, err := http.NewRequestWithContext(ctx, "POST",
			"https://iam.googleapis.com/v1/projects/"+*project+"/serviceAccounts", body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HC.Do(req)
		if err != nil {
			t.Fatalf("create signer SA: %v", err)
		}
		resp.Body.Close()
		// 409 = an earlier run made it; anything else fatal.
		if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusConflict {
			t.Fatalf("create signer SA: HTTP %d", resp.StatusCode)
		}
		// A fresh SA (and the caller's signBlob on it) can lag; probe
		// until it signs.
		probe := strings.NewReader(`{"payload":"cHJvYmU="}`)
		deadline := time.Now().Add(90 * time.Second)
		for {
			req, _ := http.NewRequestWithContext(ctx, "POST",
				"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/"+email+":signBlob",
				probe)
			req.Header.Set("Content-Type", "application/json")
			resp, err := c.HC.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("cannot signBlob as %s (HTTP %d) — grant your account: gcloud iam service-accounts add-iam-policy-binding %s --member user:$(gcloud config get account) --role roles/iam.serviceAccountTokenCreator --project %s",
					email, resp.StatusCode, email, *project)
			}
			time.Sleep(5 * time.Second)
			probe = strings.NewReader(`{"payload":"cHJvYmU="}`)
		}
	})
	return email
}

// grantBucketAccess lets the signer SA act on the conformance bucket —
// signed URLs authorize AS the signer at use time.
func grantBucketAccess(t *testing.T, c *gcpb.Client, bucket, email string) {
	t.Helper()
	ctx := context.Background()
	base := "https://storage.googleapis.com/storage/v1/b/" + bucket + "/iam"
	var policy map[string]any
	resp, err := c.HC.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonDecode(resp, &policy); err != nil {
		t.Fatal(err)
	}
	bindings, _ := policy["bindings"].([]any)
	bindings = append(bindings, map[string]any{
		"role":    "roles/storage.objectAdmin",
		"members": []string{"serviceAccount:" + email},
	})
	policy["bindings"] = bindings
	raw, _ := json.Marshal(policy)
	req, _ := http.NewRequestWithContext(ctx, "PUT", base, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HC.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode/100 != 2 {
		t.Fatalf("grant bucket access: HTTP %d", res.StatusCode)
	}
}

// gcpSignedEnv wires the signed-URL chapter against real GCS: V4 URLs
// minted via signBlob as the conformance signer SA.
func gcpSignedEnv(t *testing.T) SignedURLEnv {
	env := "conf-" + randHex(t)
	c := gcpClient(t, "conf", env)
	email := ensureSignerSA(t, c)
	c.Opts.SignerEmail = email
	physical := gcpb.BucketName(*project, "conf", env, "photos")
	makeBucket(t, c, physical)
	grantBucketAccess(t, c, physical, email)
	// Bucket IAM (the SA's objectAdmin) propagates; give first use slack.
	time.Sleep(5 * time.Second)
	return SignedURLEnv{
		Signer: gcpb.NewSigner(c),
		Client: http.DefaultClient,
		Bucket: gcpb.NewBucketStore(c),
	}
}

// TestBucketNotificationGCP proves the C5 event path end to end minus
// push delivery: a notification config on a real bucket announces a
// through-service Put on the events topic (the runtime's translation of
// the JSON_API_V1 payload is unit-pinned in pkg/runtime).
func TestBucketNotificationGCP(t *testing.T) {
	if *backend != "gcp" {
		t.Skip("gcp-only chapter (-backend=gcp)")
	}
	env := "conf-" + randHex(t)
	c := gcpClient(t, "conf", env)
	ctx := context.Background()

	topic := gcpb.EventTopicName(env, "evbkt")
	makeTopic(t, c, topic)
	makePullSub(t, c, topic+"-keep", topic)

	// The GCS service agent must publish into the topic — GCS validates
	// exactly this when the config is created.
	var agent struct {
		Email string `json:"email_address"`
	}
	resp, err := c.HC.Get("https://storage.googleapis.com/storage/v1/projects/" + *project + "/serviceAccount")
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonDecode(resp, &agent); err != nil || agent.Email == "" {
		t.Fatalf("GCS service agent: %v %q", err, agent.Email)
	}
	policy := fmt.Sprintf(`{"policy":{"bindings":[{"role":"roles/pubsub.publisher","members":["serviceAccount:%s"]}]}}`, agent.Email)
	req, _ := http.NewRequestWithContext(ctx, "POST",
		"https://pubsub.googleapis.com/v1/projects/"+*project+"/topics/"+topic+":setIamPolicy",
		strings.NewReader(policy))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HC.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode/100 != 2 {
		t.Fatalf("grant GCS agent publisher: HTTP %d", res.StatusCode)
	}

	physical := gcpb.BucketName(*project, "conf", env, "evbkt")
	makeBucket(t, c, physical)
	config := fmt.Sprintf(`{"topic":"//pubsub.googleapis.com/projects/%s/topics/%s","payload_format":"JSON_API_V1","event_types":["OBJECT_FINALIZE"]}`, *project, topic)
	deadline := time.Now().Add(60 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, "POST",
			"https://storage.googleapis.com/storage/v1/b/"+physical+"/notificationConfigs",
			strings.NewReader(config))
		req.Header.Set("Content-Type", "application/json")
		res, err := c.HC.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode/100 == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("create notification config: HTTP %d (grant propagation?)", res.StatusCode)
		}
		time.Sleep(5 * time.Second)
	}

	store := gcpb.NewBucketStore(c)
	if err := store.Put(ctx, "evbkt", "hello.txt",
		backends.BlobData{Reader: strings.NewReader("hi there"), ContentType: "text/plain", Size: 8}); err != nil {
		t.Fatalf("put: %v", err)
	}
	got := pullPayloads(t, c, topic+"-keep", 1)
	var obj struct {
		Name        string `json:"name"`
		Size        string `json:"size"`
		ContentType string `json:"contentType"`
	}
	if err := json.Unmarshal(got[0], &obj); err != nil {
		t.Fatalf("notification payload %q: %v", got[0], err)
	}
	if obj.Name != "hello.txt" || obj.Size != "8" || obj.ContentType != "text/plain" {
		t.Fatalf("notification object = %+v", obj)
	}
}
