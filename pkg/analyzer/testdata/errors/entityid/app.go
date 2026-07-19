package entityid

import "github.com/gsandoval/tako"

var links = tako.Entity[Link]("links")

type Link struct {
	Slug string
	URL  string
}

var List = tako.API("GET /api/links", list)

func list(ctx tako.Ctx, _ struct{}) ([]Link, error) { return nil, nil }
