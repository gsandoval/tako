package bodyonget

import "github.com/gsandoval/tako"

var Search = tako.API("GET /search", search)

type SearchReq struct {
	Query string `query:"q"`
	Extra string
}

func search(ctx tako.Ctx, req SearchReq) (string, error) { return "", nil }
