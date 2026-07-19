package nonconstroute

import "github.com/gsandoval/tako"

var route = "GET /hello"

var Hello = tako.API(route, hello)

var Also = tako.API(pickRoute(), hello)

func pickRoute() string { return "GET /also" }

func hello(ctx tako.Ctx, _ struct{}) (string, error) { return "hi", nil }
