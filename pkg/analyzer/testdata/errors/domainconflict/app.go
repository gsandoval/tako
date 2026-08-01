package domainconflict

import "github.com/gsandoval/tako"

// One service, one domain: the site and a handler in the same package
// claim different names, and a third handler disagrees again.
var site = tako.Site("./web", tako.WithDomain("blog.example.com"))

var (
	Read  = tako.API("GET /a", ping, tako.WithDomain("www.example.com"))
	Write = tako.API("POST /b", ping, tako.WithDomain("api.example.com"))
)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
