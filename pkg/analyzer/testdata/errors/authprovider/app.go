package authprovider

import "github.com/gsandoval/tako"

var auth = tako.Auth(tako.AuthConfig{})

var Ping = tako.API("GET /api/ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
