package gallery

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register decoders: uploads arrive in any common format
	"image/jpeg"
	"image/png"
	"sort"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"github.com/gsandoval/tako"
)

var (
	photos     = tako.Bucket("photos")
	photoStore = tako.Entity[Photo]("photos")
	thumbs     = tako.Queue[ThumbJob]("thumbnails")
	ready      = tako.Topic[PhotoReady]("photo-ready") // fan-out: MANY subscribers

	// Observability & config (spec Part 3): the feed is served through a
	// TTL cache, uploads are counted, thumbnail latency is observed, and
	// the notifier authenticates with a startup-resolved secret (.env
	// locally, Secret Manager in the cloud).
	feed     = tako.Cache[[]Photo]("feed", tako.WithTTL(time.Minute))
	notifier = tako.Secret("notifier-key")
	ups      = tako.Counter("uploads")
	thumbMs  = tako.Histogram("thumb_ms")

	// relock guards the reindex critical section (spec §2.5/§4.4): a
	// cross-entity, cross-bucket repair that must not interleave with
	// itself — across ALL instances, which is what makes it a Lock and
	// not a Tx. reruns is its ledger: every held window on record.
	relock = tako.Lock("reindex")
	reruns = tako.Entity[ReindexRun]("reindex-runs")
)

type Photo struct {
	ID      string `entity:"id" json:"id"`
	Owner   string `entity:"index" json:"owner"`
	Caption string `json:"caption"`
	Ready   bool   `entity:"index" json:"ready"` // flipped by the worker once the thumb exists
}

type ThumbJob struct {
	PhotoID string `json:"photoID"`
}

// PhotoReady announces a finished pipeline run.
type PhotoReady struct {
	PhotoID string `json:"photoID"`
	Owner   string `json:"owner"`
	Caption string `json:"caption"`
}

var (
	Upload      = tako.API("POST /api/photos", upload)
	StartUpload = tako.API("POST /api/uploads", startUpload)
	Download    = tako.API("GET /api/photos/{id}/download", download)
	Image       = tako.API("GET /api/photos/{id}/image", serveImage)
	Thumb       = tako.API("GET /api/photos/{id}/thumb", thumb)
	List        = tako.API("GET /api/photos", list)
	Objects     = tako.API("GET /api/objects", objects)
	Reindex     = tako.API("POST /api/reindex", reindex)
	ReindexRuns = tako.API("GET /api/reindex/runs", listRuns)

	// The pipeline is event-driven end to end:
	// upload → bucket event → queue → worker → topic → 2 subscribers.
	OnUploaded = tako.Subscribe(photos.Uploaded, onUploaded)
	// Batch gives the resizer throughput knobs; the footprint pins what
	// the profile can't know: image decodes are memory-hungry per
	// request, so more headroom, fewer per instance, capped fan-out.
	MakeThumb = tako.Worker(thumbs, makeThumb,
		tako.WithProfile(tako.Batch),
		tako.WithFootprint(tako.Footprint{Memory: "4Gi", Concurrency: 4, MaxInstances: 5}))
	NotifyOwner = tako.Subscribe(ready, notifyOwner)
	WarmFeed    = tako.Subscribe(ready, warmFeed)
	Cleanup     = tako.Cron("every 24h", cleanupOrphans, tako.WithSingleflight())
)

// A tako.Blob field makes the request multipart: img streams from the
// form field, caption arrives beside it.
type UploadReq struct {
	Caption string    `form:"caption"`
	Img     tako.Blob `form:"img"`
}

// upload stores the entity, then the blob; the bucket event — not the
// handler — enqueues the thumbnail work.
func upload(ctx tako.Ctx, req UploadReq) (Photo, error) {
	p := Photo{ID: tako.NewID(), Owner: ownerID(ctx), Caption: req.Caption}
	if err := photoStore.Put(ctx, p); err != nil {
		return Photo{}, err
	}
	if err := photos.Put(ctx, p.ID, req.Img); err != nil {
		return Photo{}, err
	}
	return p, nil
}

// StartUpload is the direct-transfer flow: the client gets a ticket and
// PUTs the bytes straight to storage — no 32MB request cap, no proxying
// through the service. The signed upload fires photos.Uploaded exactly
// like a Put, so the event spine below runs unchanged.
type StartUploadReq struct {
	Caption string `json:"caption"`
}

type UploadTicket struct {
	Photo     Photo  `json:"photo"`
	UploadURL string `json:"uploadURL"`
	// UploadHeaders must ride the PUT: a WithMaxSize URL signs a size
	// condition the storage backend enforces, local and cloud alike.
	UploadHeaders map[string]string `json:"uploadHeaders"`
}

// uploadLimits guard the direct-upload URL; UploadHeaders derives the
// headers the client must send from the same list.
var uploadLimits = []tako.Option{tako.WithContentType("image/*"), tako.WithMaxSize(20 << 20)}

func startUpload(ctx tako.Ctx, req StartUploadReq) (UploadTicket, error) {
	p := Photo{ID: tako.NewID(), Owner: ownerID(ctx), Caption: req.Caption}
	if err := photoStore.Put(ctx, p); err != nil {
		return UploadTicket{}, err
	}
	url, err := photos.SignedPut(ctx, p.ID, 15*time.Minute, uploadLimits...)
	return UploadTicket{Photo: p, UploadURL: url, UploadHeaders: tako.UploadHeaders(uploadLimits...)}, err
}

type DownloadReq struct {
	ID string `path:"id"`
}

type DownloadRes struct {
	URL string `json:"url"`
}

func download(ctx tako.Ctx, req DownloadReq) (DownloadRes, error) {
	url, err := photos.SignedGet(ctx, req.ID, 15*time.Minute)
	return DownloadRes{URL: url}, err
}

// ownerID is the signed-in user's ID, or "" — WithAuth enforcement
// arrives with the auth feature; SignIn provides identities in tests.
func ownerID(ctx tako.Ctx) string {
	if u, ok := ctx.MaybeUser(); ok {
		return u.ID
	}
	return ""
}

// Bucket events see EVERY successful write — including the worker's own
// _thumb output. Filtering your own writes is the standing idiom.
func onUploaded(ctx tako.Ctx, obj tako.ObjectInfo) error {
	if strings.HasSuffix(obj.Key, "_thumb") {
		return nil
	}
	ups.Add(ctx, 1)
	return thumbs.Publish(ctx, ThumbJob{PhotoID: obj.Key})
}

// One async shape everywhere: nil → ack, error → retry,
// tako.Permanent(err) → give up and dead-letter.
func makeThumb(ctx tako.Ctx, job ThumbJob) error {
	start := time.Now()
	img, err := photos.Get(ctx, job.PhotoID)
	if err != nil {
		return err // transient (object may lag the message): retry
	}
	if img.Size == 0 {
		return tako.Permanent(errors.New("empty upload")) // never retriable
	}
	if !strings.HasPrefix(img.ContentType, "image/") {
		return fmt.Errorf("unsupported content type %q", img.ContentType) // retries, then dead-letters
	}
	small, err := resize(img, 256)
	if err != nil {
		return tako.Permanent(err) // bad image bytes: don't retry
	}
	if err := photos.Put(ctx, job.PhotoID+"_thumb", small); err != nil {
		return err
	}
	if err := photoStore.Update(ctx, job.PhotoID, func(p *Photo) { p.Ready = true }); err != nil {
		return err
	}
	p, err := photoStore.Get(ctx, job.PhotoID)
	if err != nil {
		return err
	}
	thumbMs.Observe(ctx, float64(time.Since(start).Milliseconds()))
	return ready.Publish(ctx, PhotoReady{PhotoID: p.ID, Owner: p.Owner, Caption: p.Caption})
}

// resize scales a decoded image to at most width pixels wide (aspect
// preserved) and re-encodes it — PNG stays PNG, everything else becomes
// JPEG. App code, not framework: the SDK never sees image formats.
func resize(b tako.Blob, width int) (tako.Blob, error) {
	src, format, err := image.Decode(b.Reader)
	if err != nil {
		return tako.Blob{}, fmt.Errorf("decode image: %w", err)
	}
	bounds := src.Bounds()
	w := min(width, bounds.Dx())
	h := max(1, bounds.Dy()*w/bounds.Dx())
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	var buf bytes.Buffer
	contentType := "image/jpeg"
	if format == "png" {
		contentType = "image/png"
		err = png.Encode(&buf, dst)
	} else {
		err = jpeg.Encode(&buf, dst, nil)
	}
	if err != nil {
		return tako.Blob{}, err
	}
	return tako.Blob{Reader: &buf, ContentType: contentType, Size: int64(buf.Len())}, nil
}

// Two independent subscribers to one event: each gets every PhotoReady
// and retries on its own — one failing never touches the other.
func notifyOwner(ctx tako.Ctx, e PhotoReady) error {
	if strings.Contains(e.Caption, "boom") {
		return fmt.Errorf("notification service rejected %q", e.Caption)
	}
	// Value never fails here: boot refused to start if the secret was unset.
	key := notifier.Value()
	ctx.Log().Info("owner notified", "photo", e.PhotoID, "notifier", key[:2]+"…")
	return nil
}

// warmFeed repopulates the cached feeds the moment a photo becomes
// ready — the anonymous feed and the owner's — so readers rarely pay
// the query.
func warmFeed(ctx tako.Ctx, e PhotoReady) error {
	owners := []string{""}
	if e.Owner != "" {
		owners = append(owners, e.Owner)
	}
	for _, owner := range owners {
		current, err := readyPhotos(ctx, owner)
		if err != nil {
			return err
		}
		if err := feed.Put(ctx, feedKey(owner), current); err != nil {
			return err
		}
	}
	return nil
}

type ImageReq struct {
	ID string `path:"id"`
}

// Returning tako.Blob streams the object: correct Content-Type, no JSON.
func serveImage(ctx tako.Ctx, req ImageReq) (tako.Blob, error) {
	return photos.Get(ctx, req.ID) // miss → IsNotFound → 404
}

type ThumbReq struct {
	ID string `path:"id"`
}

func thumb(ctx tako.Ctx, req ThumbReq) (tako.Blob, error) {
	return photos.Get(ctx, req.ID+"_thumb")
}

// list serves the ready feed through the cache: warmFeed keeps it fresh,
// GetOrFill covers the cold start, the TTL bounds staleness. Signed-in
// users see their own feed; anonymous readers see everything ready.
func list(ctx tako.Ctx, _ struct{}) ([]Photo, error) {
	owner := ownerID(ctx)
	return feed.GetOrFill(ctx, feedKey(owner), func(ctx tako.Ctx) ([]Photo, error) {
		return readyPhotos(ctx, owner)
	})
}

func feedKey(owner string) string {
	if owner == "" {
		return "ready"
	}
	return "ready:" + owner
}

func readyPhotos(ctx tako.Ctx, owner string) ([]Photo, error) {
	q := photoStore.Query(ctx).Where("Ready", "=", true)
	if owner != "" {
		q = q.Where("Owner", "=", owner)
	}
	return q.Limit(100).All()
}

// cleanupOrphans deletes never-completed uploads: a ticket was issued
// but the bytes never arrived — or the pipeline dead-lettered — and the
// photo is still not Ready when the daily run fires. Entity and any
// object go together.
func cleanupOrphans(ctx tako.Ctx) error {
	orphans, err := photoStore.Query(ctx).Where("Ready", "=", false).Limit(1000).All()
	if err != nil {
		return err
	}
	for _, p := range orphans {
		if err := photos.Delete(ctx, p.ID); err != nil {
			return err
		}
		if err := photoStore.Delete(ctx, p.ID); err != nil {
			return err
		}
		ctx.Log().Info("cleaned orphan upload", "photo", p.ID)
	}
	return nil
}

// ReindexRun is one held reindex window — the ledger the validation
// (and any operator) reads to see that runs never overlap.
type ReindexRun struct {
	ID       string    `entity:"id" json:"id"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Photos   int       `json:"photos"`  // ready photos verified
	Demoted  int       `json:"demoted"` // thumbs found missing → Ready flipped off
}

// ReindexRes reports whether this call held the lock. started=false is
// not an error: a rebuild is already running somewhere, and this call
// collapsed into it.
type ReindexRes struct {
	Started bool       `json:"started"`
	Run     ReindexRun `json:"run"`
}

// reindex repairs the gallery under the lock: TryDo, because a caller
// finding a rebuild mid-flight should NOT queue another — the running
// one already does the work.
func reindex(ctx tako.Ctx, _ struct{}) (ReindexRes, error) {
	var run ReindexRun
	ran, err := relock.TryDo(ctx, func(ctx tako.Ctx) error {
		var err error
		run, err = rebuild(ctx)
		return err
	})
	return ReindexRes{Started: ran, Run: run}, err
}

// rebuild is the critical section: verify every ready photo's thumb
// still exists (demoting the entity when it doesn't — an operator may
// have deleted objects), then rebuild every cached feed from the
// repaired truth. Cross-entity + cross-bucket: exactly the shape that
// needs a Lock, not a Tx.
func rebuild(ctx tako.Ctx) (ReindexRun, error) {
	run := ReindexRun{ID: tako.NewID(), Started: time.Now()}
	all, err := photoStore.Query(ctx).Where("Ready", "=", true).Limit(1000).All()
	if err != nil {
		return run, err
	}
	owners := map[string]bool{"": true}
	for _, p := range all {
		ok, err := photos.Exists(ctx, p.ID+"_thumb")
		if err != nil {
			return run, err
		}
		if !ok {
			if err := photoStore.Update(ctx, p.ID, func(p *Photo) { p.Ready = false }); err != nil {
				return run, err
			}
			run.Demoted++
			continue
		}
		owners[p.Owner] = true
	}
	run.Photos = len(all)
	for owner := range owners {
		current, err := readyPhotos(ctx, owner)
		if err != nil {
			return run, err
		}
		if err := feed.Put(ctx, feedKey(owner), current); err != nil {
			return run, err
		}
	}
	run.Finished = time.Now()
	return run, reruns.Put(ctx, run)
}

func listRuns(ctx tako.Ctx, _ struct{}) ([]ReindexRun, error) {
	runs, err := reruns.Query(ctx).All()
	if err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Started.Before(runs[j].Started) })
	return runs, nil
}

func objects(ctx tako.Ctx, _ struct{}) ([]tako.ObjectInfo, error) {
	var out []tako.ObjectInfo
	it := photos.List(ctx, "")
	for o, ok := it.Next(); ok; o, ok = it.Next() {
		out = append(out, o)
	}
	return out, it.Err()
}
