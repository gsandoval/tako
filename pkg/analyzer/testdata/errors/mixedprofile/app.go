package mixedprofile

import "github.com/gsandoval/tako"

var Fast = tako.API("GET /api/fast", fast, tako.WithProfile(tako.Latency))
var Slow = tako.API("GET /api/slow", slow, tako.WithProfile(tako.Batch))

func fast(ctx tako.Ctx, _ struct{}) (string, error) { return "", nil }
func slow(ctx tako.Ctx, _ struct{}) (string, error) { return "", nil }
