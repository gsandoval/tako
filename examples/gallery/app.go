package gallery

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gsandoval/tako"
)

var (
	photos     = tako.Bucket("photos")
	photoStore = tako.Entity[Photo]("photos")
	thumbs     = tako.Queue[ThumbJob]("thumbnails")
)

type Photo struct {
	ID      string `entity:"id" json:"id"`
	Caption string `json:"caption"`
	Ready   bool   `json:"ready"` // flipped by the worker once the thumb exists
}

type ThumbJob struct {
	PhotoID string `json:"photoID"`
}

var (
	Upload  = tako.API("POST /api/photos", upload)
	Image   = tako.API("GET /api/photos/{id}/image", image)
	Thumb   = tako.API("GET /api/photos/{id}/thumb", thumb)
	List    = tako.API("GET /api/photos", list)
	Objects = tako.API("GET /api/objects", objects)

	// Exactly one worker per queue — a second is an analysis error.
	MakeThumb = tako.Worker(thumbs, makeThumb)
)

// A tako.Blob field makes the request multipart: img streams from the
// form field, caption arrives beside it.
type UploadReq struct {
	Caption string    `form:"caption"`
	Img     tako.Blob `form:"img"`
}

func upload(ctx tako.Ctx, req UploadReq) (Photo, error) {
	p := Photo{ID: tako.NewID(), Caption: req.Caption}
	if err := photos.Put(ctx, p.ID, req.Img); err != nil {
		return Photo{}, err
	}
	if err := photoStore.Put(ctx, p); err != nil {
		return Photo{}, err
	}
	if err := thumbs.Publish(ctx, ThumbJob{PhotoID: p.ID}); err != nil {
		return Photo{}, err
	}
	return p, nil
}

// One async shape everywhere: nil → ack, error → retry,
// tako.Permanent(err) → give up and dead-letter.
func makeThumb(ctx tako.Ctx, job ThumbJob) error {
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
	return photoStore.Update(ctx, job.PhotoID, func(p *Photo) { p.Ready = true })
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

func list(ctx tako.Ctx, _ struct{}) ([]Photo, error) {
	return photoStore.Query(ctx).Limit(100).All()
}

func objects(ctx tako.Ctx, _ struct{}) ([]tako.ObjectInfo, error) {
	var out []tako.ObjectInfo
	it := photos.List(ctx, "")
	for o, ok := it.Next(); ok; o, ok = it.Next() {
		out = append(out, o)
	}
	return out, it.Err()
}
