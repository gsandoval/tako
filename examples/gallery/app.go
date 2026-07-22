package gallery

import (
	"errors"
	"fmt"
	"strings"
	"time"

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
)

type Photo struct {
	ID      string `entity:"id" json:"id"`
	Caption string `json:"caption"`
	Ready   bool   `entity:"index" json:"ready"` // flipped by the worker once the thumb exists
}

type ThumbJob struct {
	PhotoID string `json:"photoID"`
}

// PhotoReady announces a finished pipeline run. Owner joins this payload
// when auth lands (F9 aligns fully with spec Part 3).
type PhotoReady struct {
	PhotoID string `json:"photoID"`
	Caption string `json:"caption"`
}

var (
	Upload      = tako.API("POST /api/photos", upload)
	StartUpload = tako.API("POST /api/uploads", startUpload)
	Download    = tako.API("GET /api/photos/{id}/download", download)
	Image       = tako.API("GET /api/photos/{id}/image", image)
	Thumb       = tako.API("GET /api/photos/{id}/thumb", thumb)
	List        = tako.API("GET /api/photos", list)
	Objects     = tako.API("GET /api/objects", objects)

	// The pipeline is event-driven end to end:
	// upload → bucket event → queue → worker → topic → 2 subscribers.
	OnUploaded  = tako.Subscribe(photos.Uploaded, onUploaded)
	MakeThumb   = tako.Worker(thumbs, makeThumb)
	NotifyOwner = tako.Subscribe(ready, notifyOwner)
	WarmFeed    = tako.Subscribe(ready, warmFeed)
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
	p := Photo{ID: tako.NewID(), Caption: req.Caption}
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
}

func startUpload(ctx tako.Ctx, req StartUploadReq) (UploadTicket, error) {
	p := Photo{ID: tako.NewID(), Caption: req.Caption}
	if err := photoStore.Put(ctx, p); err != nil {
		return UploadTicket{}, err
	}
	url, err := photos.SignedPut(ctx, p.ID, 15*time.Minute,
		tako.WithContentType("image/*"), tako.WithMaxSize(20<<20))
	return UploadTicket{Photo: p, UploadURL: url}, err
}

type DownloadReq struct {
	ID string `path:"id"`
}

func download(ctx tako.Ctx, req DownloadReq) (string, error) {
	return photos.SignedGet(ctx, req.ID, 15*time.Minute)
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
	// v0 "thumbnail" is a passthrough copy; real resizing is app code and
	// arrives with the full gallery (F9).
	thumbBlob := tako.Blob{Reader: img.Reader, ContentType: img.ContentType, Size: img.Size}
	if err := photos.Put(ctx, job.PhotoID+"_thumb", thumbBlob); err != nil {
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
	return ready.Publish(ctx, PhotoReady{PhotoID: p.ID, Caption: p.Caption})
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

// warmFeed repopulates the cached feed the moment a photo becomes ready,
// so readers rarely pay the query.
func warmFeed(ctx tako.Ctx, e PhotoReady) error {
	current, err := readyPhotos(ctx)
	if err != nil {
		return err
	}
	return feed.Put(ctx, "ready", current)
}

type ImageReq struct {
	ID string `path:"id"`
}

// Returning tako.Blob streams the object: correct Content-Type, no JSON.
func image(ctx tako.Ctx, req ImageReq) (tako.Blob, error) {
	return photos.Get(ctx, req.ID) // miss → IsNotFound → 404
}

type ThumbReq struct {
	ID string `path:"id"`
}

func thumb(ctx tako.Ctx, req ThumbReq) (tako.Blob, error) {
	return photos.Get(ctx, req.ID+"_thumb")
}

// list serves the ready feed through the cache: warmFeed keeps it fresh,
// GetOrFill covers the cold start, the TTL bounds staleness.
func list(ctx tako.Ctx, _ struct{}) ([]Photo, error) {
	return feed.GetOrFill(ctx, "ready", readyPhotos)
}

func readyPhotos(ctx tako.Ctx) ([]Photo, error) {
	return photoStore.Query(ctx).Where("Ready", "=", true).Limit(100).All()
}

func objects(ctx tako.Ctx, _ struct{}) ([]tako.ObjectInfo, error) {
	var out []tako.ObjectInfo
	it := photos.List(ctx, "")
	for o, ok := it.Next(); ok; o, ok = it.Next() {
		out = append(out, o)
	}
	return out, it.Err()
}

