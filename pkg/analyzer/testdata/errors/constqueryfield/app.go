package constqueryfield

import "github.com/gsandoval/tako"

var links = tako.Entity[Link]("links")

type Link struct {
	Slug  string `entity:"id"`
	Owner string `entity:"index"`
}

var List = tako.API("GET /api/links", list)

var f = "Owner"

func list(ctx tako.Ctx, _ struct{}) ([]Link, error) {
	return links.Query(ctx).Where(f, "=", "memo").All()
}
