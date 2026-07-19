package bindtype

import (
	"time"

	"github.com/gsandoval/tako"
)

var Since = tako.API("GET /since", since)

type SinceReq struct {
	When time.Time `query:"when"`
}

func since(ctx tako.Ctx, req SinceReq) (string, error) { return "", nil }
