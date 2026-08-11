package resourcename

import "github.com/gsandoval/tako"

// CR6: a declared name becomes a physical name across five Google
// services, each with its own charset. The intersection is lowercase
// letters, digits and hyphens.
var photos = tako.Bucket("Photo Uploads")

var thumbs = tako.Queue[string]("thumbs_v2")

var links = tako.Entity[Link]("links")

type Link struct {
	Slug string `entity:"id"`
}

var List = tako.API("GET /api/links", list)

func list(ctx tako.Ctx, _ struct{}) ([]Link, error) { return nil, nil }
