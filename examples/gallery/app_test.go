package gallery

// The user-style suite (spec §2.10): tako.NewTestApp boots fresh
// in-process backends and wires the worker, subscribers, and cron;
// endpoints are invoked with Call; DrainQueues settles the pipeline;
// AdvanceCron owns scheduled time. The HTTP and on-disk contracts
// (signed URLs, dead-letter layout) live in http_test.go.

import (
	"io"
	"strings"
	"testing"

	tako "github.com/gsandoval/tako"
)

// newApp boots a test app with the one secret gallery declares.
func newApp(t *testing.T) *tako.TestApp {
	t.Helper()
	app := tako.NewTestApp(t)
	app.SetSecret("notifier-key", "nk_test_123")
	return app
}

func TestUploadPipeline(t *testing.T) {
	app := newApp(t)
	u := app.SignIn("alice")
	ctx := app.Ctx(u)

	p, err := Upload.Call(ctx, UploadReq{Caption: "cat", Img: pngBlob(t)})
	if err != nil {
		t.Fatal(err)
	}
	if p.Owner != "alice" || p.Ready {
		t.Fatalf("photo at upload = %+v", p)
	}
	app.DrainQueues()

	got, err := photoStore.Get(ctx, p.ID)
	if err != nil || !got.Ready {
		t.Fatalf("photo after pipeline = %+v, %v", got, err)
	}
	thumb, err := photos.Get(ctx, p.ID+"_thumb")
	if err != nil {
		t.Fatalf("thumb missing: %v", err)
	}
	raw, _ := io.ReadAll(thumb.Reader)
	if bounds := decodePNG(t, raw); bounds.Dx() != 8 || bounds.Dy() != 6 {
		t.Fatalf("thumb bounds = %v (small images are not upscaled)", bounds)
	}
}

func TestFeedIsOwnerScopedAndWarm(t *testing.T) {
	app := newApp(t)
	alice := app.Ctx(app.SignIn("alice"))
	bob := app.Ctx(app.SignIn("bob"))

	if _, err := Upload.Call(alice, UploadReq{Caption: "hers", Img: pngBlob(t)}); err != nil {
		t.Fatal(err)
	}
	if _, err := Upload.Call(bob, UploadReq{Caption: "his", Img: pngBlob(t)}); err != nil {
		t.Fatal(err)
	}
	app.DrainQueues()

	mine, err := List.Call(alice, struct{}{})
	if err != nil || len(mine) != 1 || mine[0].Caption != "hers" {
		t.Fatalf("alice's feed = %+v, %v", mine, err)
	}
	all, err := List.Call(app.Ctx(), struct{}{})
	if err != nil || len(all) != 2 {
		t.Fatalf("anonymous feed = %+v, %v", all, err)
	}
}

func TestDownloadReturnsSignedURL(t *testing.T) {
	app := newApp(t)
	ctx := app.Ctx(app.SignIn("alice"))
	p, err := Upload.Call(ctx, UploadReq{Caption: "cat", Img: pngBlob(t)})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Download.Call(ctx, DownloadReq{ID: p.ID})
	if err != nil || !strings.Contains(res.URL, "/_tako/signed/") {
		t.Fatalf("download = %+v, %v", res, err)
	}
}

// TestThumbPipelineSurvivesChaos is Phase D's exit criterion: duplicate
// and redelivered ThumbJobs must leave the same end state — every photo
// ready, every thumb well-formed, the feed exact.
func TestThumbPipelineSurvivesChaos(t *testing.T) {
	app := newApp(t)
	app.Chaos()
	ctx := app.Ctx(app.SignIn("memo"))

	const n = 8
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		p, err := Upload.Call(ctx, UploadReq{Caption: "c", Img: pngBlob(t)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	app.DrainQueues()

	for _, id := range ids {
		p, err := photoStore.Get(ctx, id)
		if err != nil || !p.Ready {
			t.Fatalf("photo %s after chaos = %+v, %v", id, p, err)
		}
		thumb, err := photos.Get(ctx, id+"_thumb")
		if err != nil {
			t.Fatalf("thumb %s missing: %v", id, err)
		}
		raw, _ := io.ReadAll(thumb.Reader)
		decodePNG(t, raw)
	}
	feedNow, err := List.Call(ctx, struct{}{})
	if err != nil || len(feedNow) != n {
		t.Fatalf("feed after chaos = %d photos, %v", len(feedNow), err)
	}
}

func TestCronCleansNeverCompletedUploads(t *testing.T) {
	app := newApp(t)
	ctx := app.Ctx(app.SignIn("memo"))

	// A ticket whose bytes never arrive…
	ghost, err := StartUpload.Call(ctx, StartUploadReq{Caption: "ghost"})
	if err != nil {
		t.Fatal(err)
	}
	// …and a completed upload.
	kept, err := Upload.Call(ctx, UploadReq{Caption: "real", Img: pngBlob(t)})
	if err != nil {
		t.Fatal(err)
	}
	app.DrainQueues()

	app.AdvanceCron(Cleanup)

	if _, err := photoStore.Get(ctx, ghost.Photo.ID); !tako.IsNotFound(err) {
		t.Fatalf("ghost entity after cron: err = %v", err)
	}
	if ok, _ := photos.Exists(ctx, ghost.Photo.ID); ok {
		t.Fatal("ghost object after cron")
	}
	p, err := photoStore.Get(ctx, kept.ID)
	if err != nil || !p.Ready {
		t.Fatalf("real photo after cron = %+v, %v", p, err)
	}
	if ok, _ := photos.Exists(ctx, kept.ID+"_thumb"); !ok {
		t.Fatal("real thumb after cron")
	}
}
