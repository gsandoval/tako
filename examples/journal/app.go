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
	})

	// auth injects /_tako/api/session (Identity Platform's Google flow in the
	// cloud; the dev bar's test users locally). Sessions are revocable
	// records in the entity store.
	auth = tako.Auth(tako.AuthConfig{Google: true})
)

// Moderator is the role the desk is behind. Declared, so the name is
// read at analysis time: `tako gen graph` prints it beside the route,
// and `tako dev`'s sign-in page offers it as something to tick.
//
// In the cloud it is an Identity Platform custom claim, granted with
// auth.Users().Grant. On a laptop there is no Identity Platform, so it
// lives on the local user record and the dev page is how you pick it up.
// Either way this file only ever knows the NAME.
var Moderator = tako.Role("moderator")

// Moderators is the journal's one DOOR: who, among the people who signed
// in, may use the moderation desk.
//
// It is a declaration rather than four lines at the top of two handlers,
// which is what it used to be everywhere. Because it is declared, `tako
// gen graph` prints it per route, the generated mount names it, and the
// status page below can tell this refusal from a stale CSRF token's.
//
// Ownership is NOT here, deliberately. "Only the author may publish"
// is a question about a post, and it stays in the handler that loaded
// the post (see publish and rename). A policy answers "who may reach
// this route" — keeping the two apart is what lets one policy serve
// every route behind the same door.
//
// It used to be an email allowlist with a local escape hatch keyed on
// tako.Environment() and the reserved .test suffix. A role retires both:
// nobody maintains a list of addresses in the source, and the local way
// in is ticking a box rather than a rule that has to be right about two
// things at once.
var Moderators = tako.RequireRole(Moderator)

// Status is the page tako renders for every response it produces
// itself: a mistyped URL, a handler's tako.NotFound(), an expired CSRF
// token, a method the site does not serve, a handler that failed.
//
// It is an ordinary page — same Ctx, same templates, same layout — so
// it carries the masthead and the post count like every other page.
// SiteConfig.NotFound is still the one-line spelling for a plain 404
// (examples/blog uses it); this is what the shell needs when it is
// built from data rather than baked into the template.
var Status = tako.StatusPage(status)

// StatusView is the shell's data plus what tako can say about the
// response. A 500 carries no message by design — the failure is in the
// log under RequestID.
type StatusView struct {
	Code      int
	Heading   string
	Detail    string
	RequestID string
	Total     int64
	// DevHint is the one thing a status page can say on a laptop that
	// would be nonsense in production: how to give yourself the role you
	// were just refused for. tako.Environment() is what makes it safe to
	// write — a development affordance, never a change to the product.
	DevHint string
}

var statusHeadings = map[int]string{
	// 401 was unreachable by navigation until the moderation desk: every
	// other guarded route here is a POST, so nobody could arrive at one
	// by typing a URL. Walking /moderate signed out found this heading
	// missing and the page saying "Something went wrong" about a request
	// that had gone exactly right.
	401: "Sign in first",
	404: "Not found",
	403: "That form expired",
	405: "Not that way",
	500: "Something broke",
}

func status(ctx tako.Ctx, s tako.Status) (tako.HTML, error) {
	heading := statusHeadings[s.Code]
	if heading == "" {
		heading = "Something went wrong"
	}
	// Two things answer 403 here and they need different words. The
	// heading above is CSRF's — a form that sat too long — and it was
	// the only one until this journal had a door. Status.Policy is how
	// they are told apart: it names the policy that refused, and is
	// empty for every status tako answers on its own account. Guessing
	// from the code alone would greet a reader who is simply not a
	// moderator with "That form expired".
	hint := ""
	if s.Policy != "" {
		heading = "Not your desk"
		// The refused visitor on a laptop is nearly always the developer,
		// and the answer is one they can act on. In the cloud there is no
		// such answer, so there is no such line.
		if tako.Environment() == tako.EnvLocal {
			hint = "Running locally: sign in again at /_tako/signin and tick the role."
		}
	}
	// The count the masthead shows on every other page. A 404 that
	// drops the site's chrome is the thing this exists to prevent.
	total, err := published(posts.Query(ctx)).Count()
	if err != nil {
		total = 0
	}
	return site.Render(ctx, "status.html", StatusView{
		Code: s.Code, Heading: heading, Detail: s.Message,
		RequestID: s.RequestID, Total: total, DevHint: hint,
	})
}

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
	// Bulk unpublish: the home list's checkboxes, acted on together.
	// Declared before /p/{id}/publish is irrelevant to the mux — a
	// literal second segment beats a wildcard — but it IS why the path
	// is /p/unpublish rather than /p/{id}/unpublish.
	Unpublish = tako.Page("POST /p/unpublish", unpublishSelected, tako.Route{SignIn: true})
	// The permalink a blog actually wants, and the route that used to be
	// unshippable: a two-segment wildcard GET beside tako.Auth is the
	// pair Go's ServeMux cannot order, so declaring both panicked the
	// service at startup until tako's endpoints moved to a mux of their
	// own (FL5). /p/{id} stays — a literal first segment is more
	// specific, so the short URL and the permalink coexist.
	// A published post does not change between requests, and a reader
	// arriving from a shared link is the common case — so the permalink
	// lets shared caches keep it for five minutes instead of
	// revalidating against the entity store on every visit. Every other
	// page here answers no-cache, which is what a zero MaxAge means.
	Perma = tako.Page("GET /{year}/{slug}", perma, tako.Route{MaxAge: 5 * time.Minute})

	// The moderation desk: every post, whoever wrote it, and one action
	// on somebody else's work. Route.Allow is the whole authorization —
	// no preamble in either handler, and Allow implies SignIn, so
	// ctx.User() is guaranteed past it exactly as it is on a SignIn
	// route.
	Moderate = tako.Page("GET /moderate", moderate, tako.Route{Allow: Moderators})
	Retract  = tako.Page("POST /moderate/{id}/retract", retract, tako.Route{Allow: Moderators})
	// Who appoints the FIRST moderator? Nobody can, from inside the
	// desk — it is behind the role it hands out. So the first person
	// ever to sign in gets it, and after that the desk is the only way
	// in. A real answer to a real bootstrap problem, and the smallest
	// honest use of an account event.
	OnFirstSignIn = tako.Subscribe(auth.FirstSignIn, appointFounder)

	// Making another moderator. This is the whole of "user admin" in
	// this example, and it is deliberately behind the same door: the
	// people who can retract a post are the people who can hand that out.
	SetRole = tako.Page("POST /people/{id}/role", setRole, tako.Route{Allow: Moderators})
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

// published is a query FRAGMENT: it takes a query and adds to it, so
// tako folds its filter into whatever the caller builds (FL10). This
// used to be a closure inside home, which worked for a reason nobody
// could see — a closure is inside its enclosing function, so the
// analyzer never crossed a boundary. Promoted to a package-level
// helper it derives the same index, which is the point.
func published(q *tako.EntityQuery[Post]) *tako.EntityQuery[Post] {
	return q.Where("Published", "=", true)
}

func home(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
	list, err := published(posts.Query(ctx)).OrderBy("-Created").Limit(pageSize).All()
	if err != nil {
		return tako.HTML{}, err
	}
	// Count ignores Limit, which is the point: this is the total, not
	// the size of the page above.
	total, err := published(posts.Query(ctx)).Count()
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

// UnpublishReq binds a CHECKBOX GROUP: every box in the home list
// carries the same name, so the field is []string and every ticked id
// arrives. That is the native HTML shape of "select some rows", and
// before []string bindings it could not reach a handler at all — a
// scalar bind returns the first value, so a bulk action would have
// silently acted on one post and reported success for all of them.
//
// No JavaScript anywhere in this: the boxes are markup, the submit is a
// submit, and the answer is a 303 back to the list.
type UnpublishReq struct {
	Selected []string `form:"selected"`
}

// unpublishSelected hides every selected post the caller owns. A box
// ticked for someone else's post is skipped rather than refused: the
// list is public, the selection is a request, and the caller's own
// rows are what they may act on.
func unpublishSelected(ctx tako.Ctx, req UnpublishReq) (tako.HTML, error) {
	for _, id := range req.Selected {
		p, err := posts.Get(ctx, id)
		if tako.IsNotFound(err) {
			continue
		}
		if err != nil {
			return tako.HTML{}, err
		}
		if p.Owner != ctx.User().ID {
			continue
		}
		if err := posts.Update(ctx, id, func(p *Post) { p.Published = false }); err != nil {
			return tako.HTML{}, err
		}
	}
	return tako.Redirect("/")
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

// ModerateView is the desk: everything anyone has written, newest
// first, with what state it is in — and who may be here at all.
type ModerateView struct {
	Posts  []Post
	People []Person
	Who    string
}

// Person is one account, as the desk shows it.
type Person struct {
	ID          string
	Email       string
	IsModerator bool
}

// moderate lists every post, published or not, including other people's
// drafts — which is exactly why the route is behind a door and the home
// page is not.
func moderate(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
	list, err := posts.Query(ctx).OrderBy("-Created").Limit(pageSize).All()
	if err != nil {
		return tako.HTML{}, err
	}
	// The accounts, so a moderator can make another one. Reading the
	// list is all this needs; the grant below is what makes this service
	// ask for the write permission.
	page, err := auth.Users().List(ctx).Limit(50).Page("")
	if err != nil {
		return tako.HTML{}, err
	}
	people := make([]Person, 0, len(page.Users))
	for _, u := range page.Users {
		people = append(people, Person{ID: u.ID, Email: u.Email, IsModerator: u.HasRole(Moderator)})
	}
	return site.Render(ctx, "moderate.html", ModerateView{
		Posts: list, People: people, Who: ctx.User().Email,
	})
}

// appointFounder gives the moderator role to the first person tako ever
// mints a session for.
//
// Idempotent on purpose: auth.FirstSignIn is delivered at least once, so
// this can run twice for the same person, and the check it makes —
// "does anybody hold this role yet" — answers no only once regardless.
func appointFounder(ctx tako.Ctx, u tako.User) error {
	users := auth.Users()
	page, err := users.List(ctx).Limit(200).Page("")
	if err != nil {
		return err
	}
	for _, existing := range page.Users {
		if existing.HasRole(Moderator) {
			return nil // the site already has one
		}
	}
	ctx.Log().Info("appointing the founding moderator", "email", u.Email)
	return users.Grant(ctx, u.ID, Moderator)
}

// SetRoleReq is the toggle: which account, and whether they should hold
// the role after this.
type SetRoleReq struct {
	ID string `path:"id"`
	On string `form:"on"`
}

// setRole hands out the moderator role, or takes it back.
//
// Nothing here checks who is asking — Route.Allow did, once, at the
// declaration. What the handler is about is the act, and the act is two
// lines because tako owns the mechanism: Grant writes the claim and
// marks that person's sessions, so their next request is the one where
// it takes effect. No sign-out, no waiting for a token to expire.
func setRole(ctx tako.Ctx, req SetRoleReq) (tako.HTML, error) {
	users := auth.Users()
	var err error
	if req.On == "1" {
		err = users.Grant(ctx, req.ID, Moderator)
	} else {
		err = users.Revoke(ctx, req.ID, Moderator)
	}
	if err != nil {
		return tako.HTML{}, err
	}
	return tako.Redirect("/moderate")
}

type RetractReq struct {
	ID string `path:"id"`
}

// retract takes a post off the site whoever wrote it. Note what is NOT
// here: no allowlist check, no ctx.User() comparison, no early return.
// The door is the declaration, so this handler is only about the act.
func retract(ctx tako.Ctx, req RetractReq) (tako.HTML, error) {
	if _, err := posts.Get(ctx, req.ID); tako.IsNotFound(err) {
		return tako.HTML{}, tako.NotFound()
	} else if err != nil {
		return tako.HTML{}, err
	}
	if err := posts.Update(ctx, req.ID, func(p *Post) { p.Published = false }); err != nil {
		return tako.HTML{}, err
	}
	return tako.Redirect("/moderate")
}
