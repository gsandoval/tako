// Package queryescapes holds the query shapes tako cannot follow.
//
// Under-deriving is the failure that reaches production — the local
// backend has no indexes to be wrong about — so a query that leaves
// the analyzer's sight is refused rather than guessed at.
package queryescapes

import "github.com/gsandoval/tako"

var articles = tako.Entity[Article]("articles")

type Article struct {
	ID        string `entity:"id" json:"id"`
	State     string `entity:"index" json:"state"`
	PublishAt string `entity:"index" json:"publishAt"`
}

var List = tako.API("GET /api/articles", list)

func list(ctx tako.Ctx, _ struct{}) ([]Article, error) {
	return stash(ctx)
}

type box struct {
	q *tako.EntityQuery[Article]
}

// Stored in a struct field: whatever runs it is somewhere else.
func stash(ctx tako.Ctx) ([]Article, error) {
	var b box
	b.q = articles.Query(ctx).Where("State", "=", "published")
	return b.q.OrderBy("-PublishAt").Limit(10).All()
}

// A function value cannot be resolved to a declaration, so its
// modifiers are unknowable.
var narrow func(*tako.EntityQuery[Article]) *tako.EntityQuery[Article]

func indirect(ctx tako.Ctx) ([]Article, error) {
	return narrow(articles.Query(ctx).Where("State", "=", "published")).
		OrderBy("-PublishAt").Limit(10).All()
}
