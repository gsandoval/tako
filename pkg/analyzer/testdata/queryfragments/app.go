// Package queryfragments pins index derivation across function
// boundaries (FL10).
//
// A query value has a known type, so a function that takes or returns
// one is a FRAGMENT of a query rather than a query: it contributes no
// shape of its own, and its modifiers belong to whoever calls it. What
// this fixture holds is the set of shapes that produces.
package queryfragments

import "github.com/gsandoval/tako"

var articles = tako.Entity[Article]("articles")

type Article struct {
	ID        string `entity:"id" json:"id"`
	Section   string `entity:"index" json:"section"`
	State     string `entity:"index" json:"state"`
	Author    string `entity:"index" json:"author"`
	PublishAt string `entity:"index" json:"publishAt"`
}

var List = tako.API("GET /api/articles", list)

type ListReq struct {
	Section string `query:"section"`
}

// The fragment's filter plus the caller's own, one of which is
// conditional: two shapes, and the narrow one is the one RC1 was about.
func list(ctx tako.Ctx, req ListReq) ([]Article, error) {
	q := published(articles.Query(ctx))
	if req.Section != "" {
		q = q.Where("Section", "=", req.Section)
	}
	return q.OrderBy("-PublishAt").Limit(50).All()
}

// A fragment. It derives nothing on its own — a shape of (state) alone
// never runs.
func published(q *tako.EntityQuery[Article]) *tako.EntityQuery[Article] {
	return q.Where("State", "=", "published")
}

// Fragments compose: this one is two deep.
func byAuthor(ctx tako.Ctx, author string) ([]Article, error) {
	return authored(published(articles.Query(ctx)), author).
		OrderBy("-PublishAt").Limit(20).All()
}

func authored(q *tako.EntityQuery[Article], author string) *tako.EntityQuery[Article] {
	return q.Where("Author", "=", author)
}

// Two OrderBys in the order they are written. A composite whose sort
// properties are in the wrong order is one Datastore will not use, so
// this is the same class of failure as a missing one.
func recent(ctx tako.Ctx) ([]Article, error) {
	return articles.Query(ctx).
		Where("State", "=", "published").
		OrderBy("Section").
		OrderBy("-PublishAt").
		Limit(50).All()
}

// A fragment called inside a branch makes every modifier it carries
// optional at that call site.
func maybePublished(ctx tako.Ctx, only bool) ([]Article, error) {
	q := articles.Query(ctx).Where("Section", "=", "news")
	if only {
		q = published(q)
	}
	return q.OrderBy("-PublishAt").Limit(50).All()
}

// Never called, so it asks for nothing. Before FL10 this derived
// (author, section) — an index for a query that does not exist.
func neverCalled(q *tako.EntityQuery[Article]) *tako.EntityQuery[Article] {
	return q.Where("Author", "=", "nobody").OrderBy("Section")
}
