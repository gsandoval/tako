package pagebinding

import "github.com/gsandoval/tako"

// A page's Req binds from the request line and the form, never from a
// JSON body: a browser posts forms. An untagged field would silently
// receive nothing, so it is refused.
var Edit = tako.Page("POST /edit/{id}", edit)

type PageReq struct {
	ID    string `path:"id"`
	Title string
}

func edit(ctx tako.Ctx, req PageReq) (tako.HTML, error) { return tako.HTML{}, nil }
