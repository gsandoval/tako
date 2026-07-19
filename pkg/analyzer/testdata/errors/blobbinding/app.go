package blobbinding

import "github.com/gsandoval/tako"

// Blob field with no form tag.
var A = tako.API("POST /a", a)

type AReq struct {
	Img tako.Blob
}

func a(ctx tako.Ctx, req AReq) (string, error) { return "", nil }

// Blob on a GET route.
var B = tako.API("GET /b", b)

type BReq struct {
	Img tako.Blob `form:"img"`
}

func b(ctx tako.Ctx, req BReq) (string, error) { return "", nil }

// Blob mixed with an untagged body field.
var C = tako.API("POST /c", c)

type CReq struct {
	Img   tako.Blob `form:"img"`
	Extra string
}

func c(ctx tako.Ctx, req CReq) (string, error) { return "", nil }
