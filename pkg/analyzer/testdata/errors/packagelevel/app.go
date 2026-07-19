package packagelevel

import "github.com/gsandoval/tako"

func setup() {
	_ = tako.API("GET /hidden", hello)
}

func hello(ctx tako.Ctx, _ struct{}) (string, error) { return "hi", nil }
