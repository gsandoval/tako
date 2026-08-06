// Package indexnotes pins the pairing between a derived composite
// index and the query it was derived from.
//
// The shape is flauta's, which is the shape that caught the bug: the
// two-property query is written FIRST and sorts SECOND, so an
// implementation that sorts the tuples without carrying their sources
// along attaches every note to the wrong index. An entity whose walk
// order already matches its sorted order — which is every other
// fixture in this directory — cannot tell the two implementations
// apart.
package indexnotes

import "github.com/gsandoval/tako"

var articles = tako.Entity[Article]("articles")

type Article struct {
	ID        string `entity:"id" json:"id"`
	Section   string `entity:"index" json:"section"`
	State     string `entity:"index" json:"state"`
	PublishAt string `entity:"index" json:"publishAt"`
}

var List = tako.API("GET /api/articles", list)

type ListReq struct {
	Section string `query:"section"`
}

func list(ctx tako.Ctx, req ListReq) ([]Article, error) {
	if req.Section != "" {
		return publishedInSection(ctx, req.Section)
	}
	return publishedArticles(ctx)
}

// Written first, sorts second: (state, -publishAt).
func publishedArticles(ctx tako.Ctx) ([]Article, error) {
	return articles.Query(ctx).
		Where("State", "=", "published").
		OrderBy("-PublishAt").
		Limit(50).All()
}

// Written second, sorts first: (section, state, -publishAt).
func publishedInSection(ctx tako.Ctx, section string) ([]Article, error) {
	return articles.Query(ctx).
		Where("Section", "=", section).
		Where("State", "=", "published").
		OrderBy("-PublishAt").
		Limit(50).All()
}
