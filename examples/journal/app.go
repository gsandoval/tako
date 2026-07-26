// Package journal is the website-with-an-API showcase (spec Part 3b),
// grown feature by feature through Stage 3: W1 the static shell, W2
// pages and forms, W3 htmx fragments (this slice), W4 authored
// markdown, W5 auth — gated whole at W6. Writes are OPEN until W5
// brings auth; bodies render escaped until W4 brings markdown.
package journal

import (
	"time"

	"github.com/gsandoval/tako"
)

var (
	posts = tako.Entity[Post]("posts")

	// site serves ./web embedded in the service binary; its templates/
	// subtree backs site.Render and is never served statically.
	site = tako.Site("./web", tako.WithRedirect("/about-us", "/about"))
)

type Post struct {
	ID        string    `entity:"id" json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"` // author-supplied markdown; rendered escaped until W4
	Published bool      `entity:"index" json:"published"`
	Created   time.Time `entity:"index" json:"created"`
}

var (
	Home    = tako.Page("GET /", home)
	Read    = tako.Page("GET /p/{id}", read)
	Write   = tako.Page("GET /write", writeForm)
	Save    = tako.Page("POST /p", save)
	Publish = tako.Page("POST /p/{id}/publish", publish)
)

func home(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
	list, err := posts.Query(ctx).Where("Published", "=", true).OrderBy("-Created").Limit(50).All()
	if err != nil {
		return tako.HTML{}, err
	}
	return site.Render(ctx, "home.html", list)
}

type ReadReq struct {
	ID string `path:"id"`
}

func read(ctx tako.Ctx, req ReadReq) (tako.HTML, error) {
	p, err := posts.Get(ctx, req.ID)
	if tako.IsNotFound(err) {
		return tako.HTML{}, tako.NotFound()
	}
	if err != nil {
		return tako.HTML{}, err
	}
	return site.Render(ctx, "post.html", p)
}

func writeForm(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
	return site.Render(ctx, "write.html", nil)
}

// SaveReq binds the browser form — the CSRF token is the shim's
// problem, not this struct's.
type SaveReq struct {
	Title string `form:"title"`
	Body  string `form:"body"`
}

// save serves both worlds with one branch — the spec's centerpiece:
// under htmx the new post-card swaps into the home list without a
// navigation; without JS the same form lands on the post via a 303.
func save(ctx tako.Ctx, req SaveReq) (tako.HTML, error) {
	p := Post{ID: tako.NewID(), Title: req.Title, Body: req.Body, Published: true, Created: time.Now()}
	if err := posts.Put(ctx, p); err != nil {
		return tako.HTML{}, err
	}
	if ctx.IsHTMX() {
		return site.Fragment(ctx, "post-card.html", p)
	}
	return tako.Redirect("/p/" + p.ID) // POST-redirect-GET (303)
}

type PublishReq struct {
	ID string `path:"id"`
}

// publish toggles visibility and swaps JUST the button under htmx;
// the no-JS path returns to the post page. Open until W5's auth, like
// every write.
func publish(ctx tako.Ctx, req PublishReq) (tako.HTML, error) {
	var out Post
	err := posts.Update(ctx, req.ID, func(p *Post) { p.Published = !p.Published; out = *p })
	if tako.IsNotFound(err) {
		return tako.HTML{}, tako.NotFound()
	}
	if err != nil {
		return tako.HTML{}, err
	}
	if ctx.IsHTMX() {
		return site.Fragment(ctx, "publish-toggle.html", out)
	}
	return tako.Redirect("/p/" + out.ID)
}
