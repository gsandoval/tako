package bindpathslice

import (
	"github.com/gsandoval/tako"
)

var Tagged = tako.API("GET /posts/{tag}", tagged)

type TaggedReq struct {
	Tag []string `path:"tag"`
}

func tagged(ctx tako.Ctx, req TaggedReq) (string, error) { return "", nil }
