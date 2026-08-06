package provision

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// galleryGraph mirrors examples/gallery's AppGraph: one public API
// service (with the cron), four private async services (partition rule:
// every worker and subscriber is its own service), a bucket WITH an
// Uploaded subscriber, an entity with a composite index, a queue, a
// fan-out topic with two subscribers, and a declared secret.
func galleryGraph() *analyzer.AppGraph {
	return &analyzer.AppGraph{
		Module: "gallery",
		Services: []analyzer.Service{
			{Name: "gallery", Package: "gallery", Profile: "default"},
			{Name: "gallery-onuploaded", Package: "gallery", Profile: "default"},
			{Name: "gallery-makethumb", Package: "gallery", Profile: "batch"},
			{Name: "gallery-notifyowner", Package: "gallery", Profile: "default"},
			{Name: "gallery-warmfeed", Package: "gallery", Profile: "default"},
		},
		Resources: []analyzer.Resource{
			{Kind: "bucket", Name: "photos"},
			// Two composites whose sources differ, so the plan's
			// rendering pairs each note with its own tuple rather than
			// with a position that happens to line up (FL9).
			{Kind: "entity", Name: "photos", Type: "Photo",
				Composites: [][]string{{"owner", "ready"}, {"ready", "-created"}},
				// The query that asks for it rides to the plan (RC1).
				CompositeSources: []string{"app.go:385 readyPhotos", "app.go:404 readyPage"}},
			{Kind: "queue", Name: "thumbnails", Type: "ThumbJob"},
			{Kind: "topic", Name: "photo-ready", Type: "PhotoReady"},
			{Kind: "secret", Name: "notifier-key"},
		},
		Handlers: []analyzer.Handler{
			{Kind: "api", Var: "gallery.Upload", Service: "gallery"},
			{Kind: "api", Var: "gallery.StartUpload", Service: "gallery"},
			{Kind: "api", Var: "gallery.List", Service: "gallery"},
			{Kind: "cron", Var: "gallery.Cleanup", Service: "gallery", Schedule: "every 24h"},
			{Kind: "subscribe", Var: "gallery.OnUploaded", Service: "gallery-onuploaded",
				Binding: "photos.Uploaded", BindingKind: "bucket"},
			{Kind: "worker", Var: "gallery.MakeThumb", Service: "gallery-makethumb", Binding: "thumbnails"},
			{Kind: "subscribe", Var: "gallery.NotifyOwner", Service: "gallery-notifyowner",
				Binding: "photo-ready", BindingKind: "topic"},
			{Kind: "subscribe", Var: "gallery.WarmFeed", Service: "gallery-warmfeed",
				Binding: "photo-ready", BindingKind: "topic"},
		},
		Edges: []analyzer.Edge{
			{From: "gallery.Upload", To: "photos", ToKind: "entity", Kind: "resource", Verb: "write"},
			{From: "gallery.Upload", To: "photos", ToKind: "bucket", Kind: "resource", Verb: "write"},
			{From: "gallery.StartUpload", To: "photos", ToKind: "entity", Kind: "resource", Verb: "write"},
			{From: "gallery.StartUpload", To: "photos", ToKind: "bucket", Kind: "resource", Verb: "sign"},
			{From: "gallery.List", To: "photos", ToKind: "entity", Kind: "resource", Verb: "read"},
			{From: "gallery.Cleanup", To: "photos", ToKind: "entity", Kind: "resource", Verb: "write"},
			{From: "gallery.Cleanup", To: "photos", ToKind: "bucket", Kind: "resource", Verb: "write"},
			{From: "gallery.OnUploaded", To: "thumbnails", ToKind: "queue", Kind: "resource", Verb: "publish"},
			{From: "gallery.MakeThumb", To: "photos", ToKind: "bucket", Kind: "resource", Verb: "write"},
			{From: "gallery.MakeThumb", To: "photos", ToKind: "entity", Kind: "resource", Verb: "write"},
			{From: "gallery.MakeThumb", To: "photo-ready", ToKind: "topic", Kind: "resource", Verb: "publish"},
			{From: "gallery.NotifyOwner", To: "notifier-key", ToKind: "secret", Kind: "resource", Verb: "read"},
			{From: "gallery.WarmFeed", To: "photos", ToKind: "entity", Kind: "resource", Verb: "read"},
		},
	}
}

func galleryDesired(t *testing.T) []Object {
	t.Helper()
	cfg := Config{Project: "my-proj", ProjectNumber: "123456", Region: "us-central1", App: "gallery", Env: DefaultEnv}
	images := map[string]string{}
	for _, s := range galleryGraph().Services {
		images[s.Name] = "img@sha256:" + s.Name
	}
	desired, err := Desired(galleryGraph(), cfg, images)
	if err != nil {
		t.Fatal(err)
	}
	return desired
}

// TestGalleryPlanRender goldens the full C4 surface: fan-out
// subscriptions (two subscribers on one topic), a bucket-events topic,
// four dead topics with keep subscriptions, and grants across all seven
// target types.
func TestGalleryPlanRender(t *testing.T) {
	plan := Compute("gallery", "prod", galleryDesired(t), nil)
	want := `  + create  artifact repo tako
  + create  service account gallery-sa
  + create  service account gallery-onuploaded-sa
  + create  service account gallery-makethumb-sa
  + create  service account gallery-notifyowner-sa
  + create  service account gallery-warmfeed-sa
  + create  bucket my-proj-gallery-photos
  + create  datastore index photos (owner, ready)  ← app.go:385 readyPhotos
  + create  datastore index photos (ready, -created)  ← app.go:404 readyPage
  + create  topic thumbnails
  + create  topic photo-ready
  + create  topic photos-uploaded
  + create  topic gallery-onuploaded-dead
  + create  topic gallery-makethumb-dead
  + create  topic gallery-notifyowner-dead
  + create  topic gallery-warmfeed-dead
  + create  bucket notification my-proj-gallery-photos
  + create  pull subscription gallery-onuploaded-dead
  + create  pull subscription gallery-makethumb-dead
  + create  pull subscription gallery-notifyowner-dead
  + create  pull subscription gallery-warmfeed-dead
  + create  run service gallery
  + create  run service gallery-onuploaded
  + create  run service gallery-makethumb
  + create  run service gallery-notifyowner
  + create  run service gallery-warmfeed
  + create  iam grants bucket my-proj-gallery-photos
  + create  iam grants bucket my-proj-tako
  + create  iam grants project
  + create  iam grants run gallery
  + create  iam grants run gallery-makethumb
  + create  iam grants run gallery-notifyowner
  + create  iam grants run gallery-onuploaded
  + create  iam grants run gallery-warmfeed
  + create  iam grants secret notifier-key
  + create  iam grants serviceAccount gallery-makethumb-sa@my-proj.iam.gserviceaccount.com
  + create  iam grants serviceAccount gallery-notifyowner-sa@my-proj.iam.gserviceaccount.com
  + create  iam grants serviceAccount gallery-onuploaded-sa@my-proj.iam.gserviceaccount.com
  + create  iam grants serviceAccount gallery-sa@my-proj.iam.gserviceaccount.com
  + create  iam grants serviceAccount gallery-warmfeed-sa@my-proj.iam.gserviceaccount.com
  + create  iam grants subscription gallery-makethumb
  + create  iam grants subscription gallery-notifyowner
  + create  iam grants subscription gallery-onuploaded
  + create  iam grants subscription gallery-warmfeed
  + create  iam grants topic gallery-makethumb-dead
  + create  iam grants topic gallery-notifyowner-dead
  + create  iam grants topic gallery-onuploaded-dead
  + create  iam grants topic gallery-warmfeed-dead
  + create  iam grants topic photo-ready
  + create  iam grants topic photos-uploaded
  + create  iam grants topic thumbnails
  + create  push subscription gallery-onuploaded
  + create  push subscription gallery-makethumb
  + create  push subscription gallery-notifyowner
  + create  push subscription gallery-warmfeed
  + create  scheduler job gallery-cron-cleanup
  56 creates, 0 updates, 0 skips, 0 orphans
`
	if got := plan.Render(); got != want {
		t.Fatalf("plan:\n%s\nwant:\n%s", got, want)
	}
}

// TestGalleryFanoutAndTriggers pins C4's routing: two subscriptions on
// one topic (independent fates), the Uploaded subscriber consuming the
// bucket's events topic, and the writers of that bucket carrying the
// emit-publisher grant plus TAKO_BUCKET_EVENTS.
func TestGalleryFanoutAndTriggers(t *testing.T) {
	desired := galleryDesired(t)
	subTopics := map[string]string{}    // subscription name → source topic
	grants := map[string][]string{}     // grants object name → "role → member"
	envs := map[string]map[string]any{} // run service → env
	public := map[string]bool{}
	for _, o := range desired {
		switch o.Kind {
		case KindPushSubscription:
			subTopics[o.Name] = fmt.Sprint(o.Spec["topic"])
		case KindIamGrants:
			for _, b := range o.Spec["bindings"].([]any) {
				m := b.(map[string]any)
				grants[o.Name] = append(grants[o.Name], m["role"].(string)+" → "+m["member"].(string))
			}
		case KindRunService:
			envs[o.Name] = o.Spec["env"].(map[string]any)
			public[o.Name] = o.Spec["public"].(bool)
		}
	}

	// Fan-out: both photo-ready subscribers, each its own subscription.
	if subTopics["gallery-notifyowner"] != "photo-ready" || subTopics["gallery-warmfeed"] != "photo-ready" {
		t.Errorf("fan-out subscriptions = %v", subTopics)
	}
	// The Uploaded subscriber consumes the bucket's events topic.
	if subTopics["gallery-onuploaded"] != "photos-uploaded" {
		t.Errorf("bucket-event subscription topic = %q", subTopics["gallery-onuploaded"])
	}
	if subTopics["gallery-makethumb"] != "thumbnails" {
		t.Errorf("worker subscription topic = %q", subTopics["gallery-makethumb"])
	}

	// Bucket events are GCS's own notifications: ONLY the GCS service
	// agent publishes into the events topic — no service does (C5
	// deleted the emission path).
	sa := func(s string) string { return "serviceAccount:" + s + "-sa@my-proj.iam.gserviceaccount.com" }
	wantEmit := []string{
		"roles/pubsub.publisher → serviceAccount:service-123456@gs-project-accounts.iam.gserviceaccount.com",
	}
	if got := grants["topic photos-uploaded"]; strings.Join(got, ";") != strings.Join(wantEmit, ";") {
		t.Errorf("photos-uploaded grants = %v, want %v", got, wantEmit)
	}

	// Sign edges derive self-signBlob: the api service (StartUpload/
	// Download mint URLs) may sign as itself; no async service may.
	selfSign := "roles/iam.serviceAccountTokenCreator → " + sa("gallery")
	if got := strings.Join(grants["serviceAccount "+sa("gallery")[15:]], ";"); !strings.Contains(got, selfSign) {
		t.Errorf("api SA grants = %v, want self tokenCreator", got)
	}
	if got := strings.Join(grants["serviceAccount "+sa("gallery-warmfeed")[15:]], ";"); strings.Contains(got, "roles/iam.serviceAccountTokenCreator → "+sa("gallery-warmfeed")) {
		t.Errorf("non-signing SA has self signBlob: %v", got)
	}

	// Package-wide secret resolution: EVERY service SA reads the secret.
	if got := grants["secret notifier-key"]; len(got) != 5 {
		t.Errorf("secret grants = %v, want all 5 SAs", got)
	}

	for svc, env := range envs {
		if env["TAKO_SERVICE"] == "" {
			t.Errorf("%s missing TAKO_SERVICE", svc)
		}
	}

	// Only the API service is public; all four async services private.
	for svc, pub := range public {
		if want := svc == "gallery"; pub != want {
			t.Errorf("service %s public = %v, want %v", svc, pub, want)
		}
	}
}
