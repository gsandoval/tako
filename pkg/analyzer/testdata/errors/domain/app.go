package domain

import "github.com/gsandoval/tako"

// The shape rules refuse a domain that is not a bare FQDN. Every variant
// (scheme, port, wildcard, bad label, …) is covered exhaustively by
// TestDomainProblem; this fixture pins the TEXT and the position.
var svc = tako.Service(tako.ServiceConfig{Domain: "https://blog.example.com/"})

var Ping = tako.API("GET /ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
