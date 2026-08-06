package queryshapes

import "github.com/gsandoval/tako"

var photos = tako.Entity[Photo]("photos")

type Photo struct {
	ID      string `entity:"id"`
	Owner   string `entity:"index"`
	Ready   bool   `entity:"index"`
	Album   string `entity:"index"`
	Kind    string `entity:"index"`
	Camera  string `entity:"index"`
	Lens    string `entity:"index"`
	Created string `entity:"index"`
}

var Search = tako.API("GET /api/photos", search)

type SearchReq struct {
	Owner  string `query:"owner"`
	Album  string `query:"album"`
	Kind   string `query:"kind"`
	Camera string `query:"camera"`
	Lens   string `query:"lens"`
}

// Six conditional filters is 64 composite indexes for one function.
// Datastore allows 200 in a project and each takes minutes to build, so
// tako refuses rather than deriving a set nobody can deploy.
func search(ctx tako.Ctx, req SearchReq) ([]Photo, error) {
	q := photos.Query(ctx).Where("Ready", "=", true)
	if req.Owner != "" {
		q = q.Where("Owner", "=", req.Owner)
	}
	if req.Album != "" {
		q = q.Where("Album", "=", req.Album)
	}
	if req.Kind != "" {
		q = q.Where("Kind", "=", req.Kind)
	}
	if req.Camera != "" {
		q = q.Where("Camera", "=", req.Camera)
	}
	if req.Lens != "" {
		q = q.Where("Lens", "=", req.Lens)
	}
	if req.Owner != "" {
		q = q.OrderBy("-Created")
	}
	return q.Limit(100).All()
}
