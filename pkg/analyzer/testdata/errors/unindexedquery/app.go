package unindexedquery

import "github.com/gsandoval/tako"

var links = tako.Entity[Link]("links")

type Link struct {
	Slug  string `entity:"id"`
	URL   string
	Owner string `entity:"index"`
	Hits  int
}

var List = tako.API("GET /api/links", list)

func list(ctx tako.Ctx, _ struct{}) ([]Link, error) {
	return links.Query(ctx).
		Where("URL", "=", "x").
		OrderBy("-Hits").
		All()
}
