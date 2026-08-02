package tako_test

// Compile-time check that the SDK surface supports the spec's example apps
// as users would write them (a miniature of Part 3's gallery and Part 3b's
// journal). Nothing here runs — declaring an app must compile and must be
// side-effect-free, which TestDeclarationsAreRecords confirms by existing:
// if any constructor panicked at init, every test in this package would die
// before running.

import (
	"strings"
	"testing"
	"time"

	tako "github.com/gsandoval/tako"
)

var (
	photos     = tako.Bucket("photos")
	photoStore = tako.Entity[Photo]("photos")
	feed       = tako.Cache[FeedPage]("feed", time.Minute)
	thumbs     = tako.Queue[ThumbJob]("thumbnails")
	ready      = tako.Topic[PhotoReady]("photo-ready")
	relock     = tako.Lock("reindex")
	stripe     = tako.Secret("stripe-key")
	authn      = tako.Auth(tako.AuthConfig{Google: true, Password: true})
	site       = tako.Site("./web", tako.SiteConfig{SPA: true})
	reidx      = tako.Job[ReindexParams]("reindex")
	ups        = tako.Counter("uploads")
	lat        = tako.Histogram("thumb_ms")

	svc = tako.Service()

	oncall = tako.Email("ops@example.com")
	room   = tako.ChatSpace("spaces/AAAA1234")
	hook   = tako.Webhook("https://example.com/alerts")
	bus    = tako.PubSub(incidents)

	incidents = tako.Topic[Incident]("incidents")
)

// Incident is the app's own mirror of Google's incident JSON — tako does
// not own that shape, which is the whole point of Y1's PubSub channel.
type Incident struct {
	IncidentID string `json:"incident_id"`
	State      string `json:"state"`
	Summary    string `json:"summary"`
}

var (
	Slow    = tako.Alert(tako.Above(tako.Percentile(lat, 0.99), 2000, tako.For(5*time.Minute)), room)
	Odd     = tako.Alert(tako.Above(tako.Percentile(lat, 0.87), 1500, tako.For(5*time.Minute)), room)
	Stalled = tako.Alert(tako.Below(tako.Count(ups), 1, tako.Window{Every: 30 * time.Minute}), oncall)
	Flooded = tako.Alert(tako.Above(tako.Rate(ups), 50, tako.For(5*time.Minute)), oncall)
	Broken  = tako.Alert(tako.Above(tako.ErrorRate(svc), 0.05, tako.For(5*time.Minute)), oncall, room)
	Slowest = tako.Alert(tako.Above(tako.ErrorRate(MakeThumb), 0.05, tako.For(5*time.Minute)), hook)
	Stuck   = tako.Alert(tako.Above(tako.DeadLetters(thumbs), 0, tako.For(5*time.Minute)), oncall)
	Missed  = tako.Alert(tako.Absent(tako.Runs(Cleanup), tako.For(26*time.Hour)), oncall, bus)
)

type Photo struct {
	ID      string `entity:"id"`
	Owner   string `entity:"index"`
	Caption string
	Ready   bool `entity:"index"`
}
type FeedPage struct{ Photos []Photo }
type ThumbJob struct{ PhotoID string }
type PhotoReady struct{ PhotoID, Owner string }
type ReindexParams struct{ Since time.Time }

var (
	StartUpload = tako.API("POST /api/photos", startUpload, tako.Route{SignIn: true})
	List        = tako.API("GET /api/photos", list)
	Image       = tako.API("GET /api/photos/{id}/image", image)
	PhotoPage   = tako.Page("GET /p/{id}", photoPage)

	OnUploaded  = tako.Subscribe(photos.Uploaded, onUploaded)
	MakeThumb   = tako.Worker(thumbs, makeThumb, tako.Compute{Profile: tako.ProfileBatch})
	NotifyOwner = tako.Subscribe(ready, notifyOwner)
	Reindex     = tako.Runner(reidx, reindex, tako.RunnerConfig{Parallel: 10})
	Cleanup     = tako.Cron("every 24h", cleanupOrphans, tako.CronConfig{Singleflight: true})
	Loop        = tako.Singleton(loop)
)

type UploadTicket struct {
	Photo     Photo
	UploadURL string
}
type StartUploadReq struct {
	Caption string `json:"caption"`
}

func startUpload(ctx tako.Ctx, req StartUploadReq) (UploadTicket, error) {
	p := Photo{ID: tako.NewID(), Owner: ctx.User().ID, Caption: req.Caption}
	if err := photoStore.Put(ctx, p); err != nil {
		return UploadTicket{}, err
	}
	url, err := photos.SignedPut(ctx, p.ID, 15*time.Minute,
		tako.PutPolicy{ContentType: "image/*", MaxSize: 20 << 20})
	return UploadTicket{Photo: p, UploadURL: url}, err
}

func onUploaded(ctx tako.Ctx, obj tako.ObjectInfo) error {
	if strings.HasSuffix(obj.Key, "_thumb") {
		return nil
	}
	ups.Add(ctx, 1)
	return thumbs.Publish(ctx, ThumbJob{PhotoID: obj.Key})
}

func makeThumb(ctx tako.Ctx, job ThumbJob) error {
	img, err := photos.Get(ctx, job.PhotoID)
	if err != nil {
		return err
	}
	if img.Size > 20<<20 {
		return tako.Permanent(err)
	}
	if err := photos.Put(ctx, job.PhotoID+"_thumb", img); err != nil {
		return err
	}
	if err := photoStore.Update(ctx, job.PhotoID, func(p *Photo) { p.Ready = true }); err != nil {
		return err
	}
	p, _ := photoStore.Get(ctx, job.PhotoID)
	lat.Observe(ctx, 12.5)
	return ready.Publish(ctx, PhotoReady{PhotoID: p.ID, Owner: p.Owner})
}

func notifyOwner(ctx tako.Ctx, e PhotoReady) error { return nil }

func reindex(ctx tako.Ctx, p ReindexParams) error {
	return relock.Do(ctx, func(ctx tako.Ctx) error {
		_ = stripe.Value()
		held, err := relock.TryDo(ctx, func(tako.Ctx) error { return nil })
		_ = held
		return err
	})
}

func cleanupOrphans(ctx tako.Ctx) error { return reidx.Run(ctx, ReindexParams{}) }
func loop(ctx tako.Ctx) error           { return nil }

func list(ctx tako.Ctx, _ struct{}) ([]Photo, error) {
	q := photoStore.Query(ctx).Where("Ready", "=", true)
	if u, ok := ctx.MaybeUser(); ok {
		q = q.Where("Owner", "=", u.ID)
	}
	return q.OrderBy("-Created").Limit(100).All()
}

type ImageReq struct {
	ID string `path:"id"`
}

func image(ctx tako.Ctx, req ImageReq) (tako.Blob, error) {
	return photos.Get(ctx, req.ID+"_thumb")
}

type PhotoPageReq struct {
	ID string `path:"id"`
}

func photoPage(ctx tako.Ctx, req PhotoPageReq) (tako.HTML, error) {
	p, err := photoStore.Get(ctx, req.ID)
	if tako.IsNotFound(err) {
		return tako.HTML{}, tako.NotFound()
	}
	if !p.Ready {
		return tako.Redirect("/p/" + p.ID)
	}
	if ctx.IsHTMX() {
		return site.Fragment(ctx, "photo-card.html", p)
	}
	return site.Render(ctx, "photo.html", p)
}

// exercise remaining surface shapes without executing them
var _ = func() error {
	var ctx tako.Ctx
	_, _ = photos.SignedGet(ctx, "k", time.Minute)
	_, _ = photos.Exists(ctx, "k")
	_ = photos.Delete(ctx, "k")
	_ = photos.List(ctx, "pre")
	_, _ = feed.GetOrFill(ctx, "k", func(tako.Ctx) (FeedPage, error) { return FeedPage{}, nil })
	_ = feed.Put(ctx, "k", FeedPage{})
	_, _ = feed.Get(ctx, "k")
	_ = feed.Delete(ctx, "k")
	_, _ = feed.Exists(ctx, "k")
	_, _ = photoStore.Exists(ctx, "id")
	_ = photoStore.Delete(ctx, "id")
	_, _ = photoStore.Query(ctx).First()
	_ = photoStore.Query(ctx).Iter()
	_ = photoStore.Tx(ctx, func(tx tako.EntityTx[Photo]) error {
		a, _ := tx.Get("id1")
		b, _ := tx.Get("id2")
		_ = tx.Put(a)
		_ = tx.Put(b)
		return tx.Delete("id3")
	})
	_ = thumbs.PublishAfter(ctx, ThumbJob{}, 4*time.Hour)
	_, _ = site.Markdown(ctx, "# hi")
	return tako.Errorf(409, "conflict on %s", "x")
}

func TestDeclarationsAreRecords(t *testing.T) {
	// Reaching this test at all proves every constructor above ran at
	// package init without side effects or panics — a panicking
	// constructor would kill the whole test binary before any test ran.
}

// Uses of Endpoint.Call and TestApp compile but must not run until S0.2+.
var _ = func() {
	var t *testing.T
	app := tako.NewTestApp(t)
	u := app.SignIn("alice")
	_, _ = StartUpload.Call(app.Ctx(u), StartUploadReq{Caption: "cat"})
	app.DrainQueues()
	app.AdvanceCron(Cleanup)
	app.Chaos()
}
