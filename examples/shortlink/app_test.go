package shortlink

import (
	"testing"

	"github.com/gsandoval/tako"
)

// This suite is written the way a Tako user writes tests (spec §2.10):
// tako.NewTestApp boots fresh in-process backends and wires the worker;
// endpoints are invoked with Call; DrainQueues settles async work.

func TestCreateResolveList(t *testing.T) {
	app := tako.NewTestApp(t)
	u := app.SignIn("memo")
	ctx := app.Ctx(u)

	if _, err := Create.Call(ctx, CreateReq{Slug: "gh", URL: "https://github.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create.Call(ctx, CreateReq{Slug: "hn", URL: "https://news.ycombinator.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create.Call(app.Ctx(app.SignIn("alex")), CreateReq{Slug: "x", URL: "https://x.com"}); err != nil {
		t.Fatal(err)
	}

	// Resolving returns the link; the click counts once queues drain.
	got, err := Resolve.Call(ctx, ResolveReq{Slug: "gh"})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://github.com" {
		t.Fatalf("resolve = %+v", got)
	}
	if _, err := Resolve.Call(ctx, ResolveReq{Slug: "gh"}); err != nil {
		t.Fatal(err)
	}
	app.DrainQueues()
	if l, _ := links.Get(app.Ctx(), "gh"); l.Hits != 2 {
		t.Fatalf("hits after two resolves = %d", l.Hits)
	}

	// List filters by owner (from SignIn), newest first.
	all, err := List.Call(ctx, ListReq{Owner: "memo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Slug != "hn" || all[1].Slug != "gh" {
		t.Fatalf("list = %+v", all)
	}
}

// TestClicksCountExactlyOnceUnderChaos is the reason chaos mode exists:
// deliveries duplicate, reorder, and redeliver after ack, and the tally
// must STILL be exact. Remove countClick's idempotency claim and this
// fails — with a seed that reproduces the run.
func TestClicksCountExactlyOnceUnderChaos(t *testing.T) {
	app := tako.NewTestApp(t)
	app.Chaos()
	ctx := app.Ctx(app.SignIn("memo"))

	if _, err := Create.Call(ctx, CreateReq{Slug: "go", URL: "https://go.dev"}); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if _, err := Resolve.Call(ctx, ResolveReq{Slug: "go"}); err != nil {
			t.Fatal(err)
		}
	}
	app.DrainQueues()

	l, err := links.Get(app.Ctx(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if l.Hits != 20 {
		t.Fatalf("hits = %d, want exactly 20", l.Hits)
	}
}

// TestAppsAreIsolated: two test apps, no shared state — contexts carry
// their app's backends.
func TestAppsAreIsolated(t *testing.T) {
	a := tako.NewTestApp(t)
	b := tako.NewTestApp(t)
	if _, err := Create.Call(a.Ctx(), CreateReq{Slug: "gh", URL: "https://github.com", Owner: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := links.Get(b.Ctx(), "gh"); !tako.IsNotFound(err) {
		t.Fatalf("app b sees app a's link (err = %v)", err)
	}
	if _, err := links.Get(a.Ctx(), "gh"); err != nil {
		t.Fatalf("app a lost its own link: %v", err)
	}
}

func TestDuplicateSlugIs409(t *testing.T) {
	app := tako.NewTestApp(t)
	ctx := app.Ctx()
	if _, err := Create.Call(ctx, CreateReq{Slug: "gh", URL: "https://github.com"}); err != nil {
		t.Fatal(err)
	}
	_, err := Create.Call(ctx, CreateReq{Slug: "gh", URL: "https://elsewhere.com"})
	if err == nil {
		t.Fatal("duplicate slug accepted")
	}
	he, ok := err.(interface{ HTTPStatus() int })
	if !ok || he.HTTPStatus() != 409 {
		t.Fatalf("err = %v, want 409", err)
	}
	// The transaction rolled back: original URL survives.
	l, err := links.Get(ctx, "gh")
	if err != nil || l.URL != "https://github.com" {
		t.Fatalf("after conflict: %+v, %v", l, err)
	}
}

func TestResolveMissingIsNotFound(t *testing.T) {
	app := tako.NewTestApp(t)
	_, err := Resolve.Call(app.Ctx(), ResolveReq{Slug: "nope"})
	if !tako.IsNotFound(err) {
		t.Fatalf("err = %v, want IsNotFound", err)
	}
}

func TestValidation(t *testing.T) {
	app := tako.NewTestApp(t)
	_, err := Create.Call(app.Ctx(), CreateReq{Slug: "", URL: ""})
	he, ok := err.(interface{ HTTPStatus() int })
	if !ok || he.HTTPStatus() != 422 {
		t.Fatalf("err = %v, want 422", err)
	}
}
