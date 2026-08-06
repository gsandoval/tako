// Package blog is the static-generator showcase (S2): the site itself
// is built by Hugo — tako runs the build (WithBuild) and ships the
// output — while htmx "islands" in the generated pages call back into
// tako fragments for the one dynamic thing a blog needs: comments.
// Static where static wins, dynamic exactly where it doesn't.
package blog

import (
	"strings"
	"time"

	"github.com/gsandoval/tako"
)

var (
	comments = tako.Entity[Comment]("comments")

	// Hugo owns ./site (content, layouts, static); tako owns the build:
	// dev reruns it on change, deploy embeds the output in the binary.
	// The fragment templates ride Hugo's static/ dir into
	// public/templates — the one subtree a Site never serves raw.
	site = tako.Site("./site/public",
		tako.SiteConfig{
			Build: tako.Build{Command: "cd site && hugo --minify", Out: "./site/public"},
			// The one-line spelling: name a page, get a 404. Hugo emits
			// 404.html already, so this is the whole of it. A site that
			// needs the shell to carry data declares a tako.StatusPage
			// instead (examples/journal).
			NotFound: "404.html",
		})
)

// Comment hangs off a post by the post's own URL path — the one stable
// identity a static generator gives every page.
type Comment struct {
	ID      string    `entity:"id" json:"id"`
	Post    string    `entity:"index" json:"post"`
	Name    string    `json:"name"`
	Body    string    `json:"body"`
	Created time.Time `entity:"index" json:"created"`
}

var (
	Comments   = tako.Page("GET /frag/comments", listComments)
	AddComment = tako.Page("POST /frag/comments", addComment)
)

type ListReq struct {
	Post string `query:"post"`
}

// island is what the templates render: one post's comments plus the
// form that grows them.
type island struct {
	Post     string
	Comments []Comment
}

func listComments(ctx tako.Ctx, req ListReq) (tako.HTML, error) {
	if err := postPath(req.Post); err != nil {
		return tako.HTML{}, err
	}
	list, err := comments.Query(ctx).Where("Post", "=", req.Post).OrderBy("Created").Limit(100).All()
	if err != nil {
		return tako.HTML{}, err
	}
	return site.Fragment(ctx, "comments.html", island{Post: req.Post, Comments: list})
}

type AddReq struct {
	Post string `form:"post"`
	Name string `form:"name"`
	Body string `form:"body"`
}

// addComment serves both worlds with one branch: under htmx the whole
// island re-renders in place; without JS the same form 303s back to
// the static post page it came from.
func addComment(ctx tako.Ctx, req AddReq) (tako.HTML, error) {
	if err := postPath(req.Post); err != nil {
		return tako.HTML{}, err
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Body) == "" {
		return tako.HTML{}, tako.Errorf(422, "a comment needs a name and a body")
	}
	c := Comment{ID: tako.NewID(), Post: req.Post, Name: req.Name, Body: req.Body, Created: time.Now()}
	if err := comments.Put(ctx, c); err != nil {
		return tako.HTML{}, err
	}
	if ctx.IsHTMX() {
		return listComments(ctx, ListReq{Post: req.Post})
	}
	return tako.Redirect(req.Post) // POST-redirect-GET, back to the static page
}

// postPath accepts only site-local paths ("/posts/hello-tako/") — the
// value round-trips into a redirect, and an open redirect is not a
// feature.
func postPath(p string) error {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.Contains(p, "..") {
		return tako.Errorf(422, "post must be a site-local path like /posts/my-post/")
	}
	return nil
}
