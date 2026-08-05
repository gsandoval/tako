// Package journal is the website-with-an-API showcase (spec Part 3b),
// grown feature by feature through Stage 3: W1 the static shell, W2
// pages and forms, W3 htmx fragments, W4 authored markdown, W5 auth
// (this slice) — gated whole at W6. Reads are public; writes require a
// signed-in user, and only the author may toggle publication.
package journal

import (
	"time"

	"github.com/gsandoval/tako"
)

var (
	posts = tako.Entity[Post]("posts")

	// moved records a slug a post used to live at. A rename cannot just
	// overwrite the id: every link anyone ever shared points at the old
	// one, and a 404 is how a URL dies. This is the store the permanent
	// redirect is served FROM, which is the whole reason a per-request
	// redirect had to exist — SiteConfig.Redirects is a map literal read
	// at analysis time, and no analysis knows a slug a user will type
	// next week.
	moved = tako.Entity[MovedURL]("moved_urls")

	// site serves ./web embedded in the service binary; its templates/
	// subtree backs site.Render and is never served statically.
	site = tako.Site("./web", tako.SiteConfig{
		Redirects: map[string]string{"/about-us": "/about"},
		// One field, both ways a request can miss: a mistyped URL that
		// matches no static file, and a handler returning
		// tako.NotFound() for a post that is not there. To a visitor
		// they are the same experience, and only one of them could ever
		// have been answered by something a handler returns.
		NotFound: "404.html",
	})

	// auth injects /_tako/api/session (Identity Platform's Google flow in the
	// cloud; the dev bar's test users locally). Sessions are revocable
	// records in the entity store.
	auth = tako.Auth(tako.AuthConfig{Google: true})
)

// MovedURL is one retired slug and where it went. Keyed by the OLD
// slug, because that is what an incoming request has.
type MovedURL struct {
	ID string `entity:"id" json:"id"` // the old slug
	To string `json:"to"`             // the current one
}

type Post struct {
	ID        string    `entity:"id" json:"id"`
	Owner     string    `json:"owner"` // the author's uid; only they may toggle publication
	Title     string    `json:"title"`
	Body      string    `json:"body"` // author-supplied markdown; rendered escaped until W4
	Published bool      `entity:"index" json:"published"`
	Created   time.Time `entity:"index" json:"created"`
}

var (
	Home    = tako.Page("GET /", home)
	Read    = tako.Page("GET /p/{id}", read)
	Write   = tako.Page("GET /write", writeForm)
	Save    = tako.Page("POST /p", save, tako.Route{SignIn: true})
	Publish = tako.Page("POST /p/{id}/publish", publish, tako.Route{SignIn: true})
	Rename  = tako.Page("POST /p/{id}/rename", rename, tako.Route{SignIn: true})
	// The permalink a blog actually wants, and the route that used to be
	// unshippable: a two-segment wildcard GET beside tako.Auth is the
	// pair Go's ServeMux cannot order, so declaring both panicked the
	// service at startup until tako's endpoints moved to a mux of their
	// own (FL5). /p/{id} stays — a literal first segment is more
	// specific, so the short URL and the permalink coexist.
	Perma = tako.Page("GET /{year}/{slug}", perma)
)

// HomeView is the home page's data: one page of posts, and how many
// there are in total.
//
// The total is what Count is for. Before it, the page could say "here
// are 50 posts" and had no way to say "of 214" — the only way to learn
// the total was to read every matching entity and throw the bodies
// away, which costs a read per post to render one number.
type HomeView struct {
	Posts []Post
	Total int64
	Shown int
}

const pageSize = 50

func home(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
	published := func() *tako.EntityQuery[Post] {
		return posts.Query(ctx).Where("Published", "=", true)
	}
	list, err := published().OrderBy("-Created").Limit(pageSize).All()
	if err != nil {
		return tako.HTML{}, err
	}
	// Count ignores Limit, which is the point: this is the total, not
	// the size of the page above.
	total, err := published().Count()
	if err != nil {
		return tako.HTML{}, err
	}
	return site.Render(ctx, "home.html", HomeView{Posts: list, Total: total, Shown: len(list)})
}

type ReadReq struct {
	ID string `path:"id"`
}

func read(ctx tako.Ctx, req ReadReq) (tako.HTML, error) {
	p, err := posts.Get(ctx, req.ID)
	if tako.IsNotFound(err) {
		// Not here — but it may have been. A slug that moved answers
		// 301 rather than 404, so shared links and search results
		// survive a rename instead of rotting.
		if m, mErr := moved.Get(ctx, req.ID); mErr == nil {
			return tako.RedirectPermanent("/p/" + m.To)
		}
		return tako.HTML{}, tako.NotFound()
	}
	if err != nil {
		return tako.HTML{}, err
	}
	return site.Render(ctx, "post.html", p)
}

// PermaReq binds the permalink's two segments. The year is not used to
// find the post — the slug is the id — but it is part of the URL a
// reader shares, so it is bound rather than ignored.
type PermaReq struct {
	Year string `path:"year"`
	Slug string `path:"slug"`
}

func perma(ctx tako.Ctx, req PermaReq) (tako.HTML, error) {
	return read(ctx, ReadReq{ID: req.Slug})
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
	p := Post{ID: tako.NewID(), Owner: ctx.User().ID, Title: req.Title, Body: req.Body, Published: true, Created: time.Now()}
	if err := posts.Put(ctx, p); err != nil {
		return tako.HTML{}, err
	}
	if ctx.IsHTMX() {
		return site.Fragment(ctx, "post-card.html", p)
	}
	return tako.Redirect("/p/" + p.ID) // POST-redirect-GET (303)
}

type RenameReq struct {
	ID   string `path:"id"`
	Slug string `form:"slug"`
}

// rename moves a post to a new slug and leaves a signpost at the old
// one.
//
// The order matters and is the whole care of it: write the new post,
// then the forwarding record, then delete the old. A crash anywhere
// leaves the post reachable at one slug or both, never at neither.
func rename(ctx tako.Ctx, req RenameReq) (tako.HTML, error) {
	current, err := posts.Get(ctx, req.ID)
	if tako.IsNotFound(err) {
		return tako.HTML{}, tako.NotFound()
	}
	if err != nil {
		return tako.HTML{}, err
	}
	if current.Owner != ctx.User().ID {
		return tako.HTML{}, tako.Errorf(403, "only the author can rename")
	}
	if req.Slug == "" || req.Slug == req.ID {
		return tako.HTML{}, tako.Errorf(400, "a rename needs a new slug")
	}
	if _, err := posts.Get(ctx, req.Slug); err == nil {
		return tako.HTML{}, tako.Errorf(409, "that slug is taken")
	}
	next := current
	next.ID = req.Slug
	if err := posts.Put(ctx, next); err != nil {
		return tako.HTML{}, err
	}
	if err := moved.Put(ctx, MovedURL{ID: req.ID, To: req.Slug}); err != nil {
		return tako.HTML{}, err
	}
	if err := posts.Delete(ctx, req.ID); err != nil {
		return tako.HTML{}, err
	}
	// A plain 303: this response is about the write that just happened,
	// not a statement that /p/<old> has permanently moved. The 301 for
	// that lives in read, where the old slug is actually requested.
	return tako.Redirect("/p/" + next.ID)
}

type PublishReq struct {
	ID string `path:"id"`
}

// publish toggles visibility and swaps JUST the button under htmx;
// the no-JS path returns to the post page. Route{SignIn: true} guarantees a user;
// ownership is the handler's own rule.
func publish(ctx tako.Ctx, req PublishReq) (tako.HTML, error) {
	current, err := posts.Get(ctx, req.ID)
	if tako.IsNotFound(err) {
		return tako.HTML{}, tako.NotFound()
	}
	if err != nil {
		return tako.HTML{}, err
	}
	if current.Owner != ctx.User().ID {
		return tako.HTML{}, tako.Errorf(403, "only the author can publish")
	}
	var out Post
	if err := posts.Update(ctx, req.ID, func(p *Post) { p.Published = !p.Published; out = *p }); err != nil {
		return tako.HTML{}, err
	}
	if ctx.IsHTMX() {
		return site.Fragment(ctx, "publish-toggle.html", out)
	}
	return tako.Redirect("/p/" + out.ID)
}
