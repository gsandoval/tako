package gallery

import "github.com/gsandoval/tako"

var (
	photos     = tako.Bucket("photos")
	photoStore = tako.Entity[Photo]("photos")
)

type Photo struct {
	ID      string `entity:"id" json:"id"`
	Caption string `json:"caption"`
}

var (
	Upload  = tako.API("POST /api/photos", upload)
	Image   = tako.API("GET /api/photos/{id}/image", image)
	List    = tako.API("GET /api/photos", list)
	Objects = tako.API("GET /api/objects", objects)
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
	return p, nil
}

type ImageReq struct {
	ID string `path:"id"`
}

// Returning tako.Blob streams the object: correct Content-Type, no JSON.
func image(ctx tako.Ctx, req ImageReq) (tako.Blob, error) {
	return photos.Get(ctx, req.ID) // miss → IsNotFound → 404
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
