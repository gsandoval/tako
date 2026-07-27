package notyet

import "github.com/gsandoval/tako"

var auth = tako.Auth()

var site = tako.Site("./web", tako.WithDomain("shop.example.com"))

var Ping = tako.API("GET /api/ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
