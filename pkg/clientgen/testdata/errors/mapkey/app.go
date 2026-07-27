package mapkey

import "github.com/gsandoval/tako"

var Counts = tako.API("GET /counts", counts)

type CountsRes struct {
	ByYear map[int]int
}

func counts(ctx tako.Ctx, req struct{}) (CountsRes, error) { return CountsRes{}, nil }
