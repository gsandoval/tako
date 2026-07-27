package collision

import "github.com/gsandoval/tako"

// URLFor and UrlFor both camel-case to the client method urlFor.
var URLFor = tako.API("GET /url", urlFor)
var UrlFor = tako.API("GET /url2", urlFor2)

func urlFor(ctx tako.Ctx, req struct{}) (string, error)  { return "", nil }
func urlFor2(ctx tako.Ctx, req struct{}) (string, error) { return "", nil }
