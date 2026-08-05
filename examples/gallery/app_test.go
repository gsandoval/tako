package gallery

// The user-style suite (spec §2.10): tako.NewTestApp boots fresh
// in-process backends and wires the worker, subscribers, and cron;
// endpoints are invoked with Call; DrainQueues settles the pipeline;
// AdvanceCron owns scheduled time. The HTTP and on-disk contracts
// (signed URLs, dead-letter layout) live in http_test.go.

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

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

	mine, err := List.Call(alice, ListReq{})
	if err != nil || len(mine.Photos) != 1 || mine.Photos[0].Caption != "hers" {
		t.Fatalf("alice's feed = %+v, %v", mine, err)
	}
	all, err := List.Call(app.Ctx(), ListReq{})
	if err != nil || len(all.Photos) != 2 {
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
	if err != nil || !strings.Contains(res.URL, "/_tako/api/signed/") {
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
	feedNow, err := List.Call(ctx, ListReq{})
	if err != nil || len(feedNow.Photos) != n {
		t.Fatalf("feed after chaos = %d photos, %v", len(feedNow.Photos), err)
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

// Paging the feed, with a write landing mid-walk.
//
// This is the case the cursor design exists for. A keyset cursor names
// the last row seen, so a photo created DURING the walk — which sorts
// newest-first, ahead of everything already seen — does not shift the
// remaining pages and does not make a row appear twice. An offset cursor
// would do both.
func TestFeedPagesThroughEverythingWithAWriteMidWalk(t *testing.T) {
	app := tako.NewTestApp(t)
	app.SetSecret("notifier-key", "nk_test_123")
	ctx := app.Ctx()

	const total = feedPage + 5
	for i := 0; i < total; i++ {
		if _, err := Upload.Call(ctx, UploadReq{
			Caption: fmt.Sprintf("photo %02d", i), Img: pngBlob(t)}); err != nil {
			t.Fatal(err)
		}
		// Created is the sort key and time.Now() can repeat inside one
		// tick; the id tiebreak covers that, but spacing them keeps the
		// expected ORDER of this test readable.
		time.Sleep(time.Millisecond)
	}
	app.DrainQueues()

	seen := map[string]int{}
	var pages int
	cursor := ""
	for {
		page, err := List.Call(ctx, ListReq{Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		pages++
		for _, p := range page.Photos {
			seen[p.ID]++
		}
		if pages == 1 {
			if len(page.Photos) != feedPage {
				t.Fatalf("first page = %d photos, want a full page of %d", len(page.Photos), feedPage)
			}
			// Mid-walk write: newest, so it sorts BEFORE everything the
			// first page returned.
			if _, err := Upload.Call(ctx, UploadReq{Caption: "interloper", Img: pngBlob(t)}); err != nil {
				t.Fatal(err)
			}
			app.DrainQueues()
		}
		if page.Cursor == "" {
			break
		}
		cursor = page.Cursor
		if pages > 10 {
			t.Fatal("paging did not terminate — an empty cursor is the only stop condition")
		}
	}

	for id, n := range seen {
		if n != 1 {
			t.Errorf("photo %s appeared %d times across the walk — a keyset cursor must not repeat a row", id, n)
		}
	}
	if len(seen) != total {
		t.Errorf("walked %d photos over %d pages, want the %d that existed when the walk began",
			len(seen), pages, total)
	}
}

// Page refuses without an order, in the SAME words on both backends.
// A query that pages correctly in development and silently skips rows in
// the cloud is the failure this refusal exists to prevent, so it must be
// impossible to write rather than merely discouraged.
func TestPagingWithoutAnOrderIsRefused(t *testing.T) {
	app := tako.NewTestApp(t)
	_, err := photoStore.Query(app.Ctx()).Where("Ready", "=", true).Limit(5).Page("")
	if err == nil {
		t.Fatal("paging with no OrderBy must be refused")
	}
	if !strings.Contains(err.Error(), "OrderBy") {
		t.Errorf("error %q does not name the thing to add", err)
	}
	_, err = photoStore.Query(app.Ctx()).OrderBy("-Created").Page("")
	if err == nil {
		t.Fatal("paging with no Limit must be refused")
	}
	if !strings.Contains(err.Error(), "Limit") {
		t.Errorf("error %q does not name the thing to add", err)
	}
}
