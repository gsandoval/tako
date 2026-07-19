package unexportedreq

import "github.com/gsandoval/tako"

var Greet = tako.API("GET /greet/{name}", greet)

type greetReq struct {
	Name string `path:"name"`
}

func greet(ctx tako.Ctx, req greetReq) (string, error) { return "", nil }
