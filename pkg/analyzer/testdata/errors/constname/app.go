package constname

import "github.com/gsandoval/tako"

var n = "links"

var links = tako.Entity[Link](n)

type Link struct {
	Slug string `entity:"id"`
}

var List = tako.API("GET /api/links", list)

func list(ctx tako.Ctx, _ struct{}) ([]Link, error) { return nil, nil }
