package badtype

import "github.com/gsandoval/tako"

var Weird = tako.API("GET /weird", weird)

type WeirdRes struct {
	Updates chan int
}

func weird(ctx tako.Ctx, req struct{}) (WeirdRes, error) { return WeirdRes{}, nil }
