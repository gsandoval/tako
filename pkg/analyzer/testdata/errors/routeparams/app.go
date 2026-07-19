package routeparams

import "github.com/gsandoval/tako"

var Greet = tako.API("GET /greet/{name}", greet)

// GreetReq tags a parameter the route does not declare, and misses the
// one it does.
type GreetReq struct {
	Nick string `path:"nick"`
}

func greet(ctx tako.Ctx, req GreetReq) (string, error) { return "", nil }
