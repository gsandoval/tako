package shortlink

import (
	"testing"

	"github.com/gsandoval/tako"
	"github.com/gsandoval/tako/pkg/runtime"
)

// boot wires a fresh local backend per test. (tako.NewTestApp replaces
// this plumbing in F7.)
func boot(t *testing.T) tako.Ctx {
	t.Helper()
	if err := runtime.BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return runtime.NewCtx(t.Context())
}

func TestCreateResolveList(t *testing.T) {
	ctx := boot(t)

	if _, err := create(ctx, CreateReq{Slug: "gh", URL: "https://github.com", Owner: "memo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := create(ctx, CreateReq{Slug: "hn", URL: "https://news.ycombinator.com", Owner: "memo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := create(ctx, CreateReq{Slug: "x", URL: "https://x.com", Owner: "alex"}); err != nil {
		t.Fatal(err)
	}

	// Resolving increments hits and returns the link.
	got, err := Resolve.Call(ctx, ResolveReq{Slug: "gh"})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://github.com" || got.Hits != 1 {
		t.Fatalf("resolve = %+v", got)
	}
	if got, _ = Resolve.Call(ctx, ResolveReq{Slug: "gh"}); got.Hits != 2 {
		t.Fatalf("hits after second resolve = %d", got.Hits)
	}

	// List filters by owner, newest first.
	all, err := List.Call(ctx, ListReq{Owner: "memo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Slug != "hn" || all[1].Slug != "gh" {
		t.Fatalf("list = %+v", all)
	}
}

func TestDuplicateSlugIs409(t *testing.T) {
	ctx := boot(t)
	if _, err := create(ctx, CreateReq{Slug: "gh", URL: "https://github.com"}); err != nil {
		t.Fatal(err)
	}
	_, err := create(ctx, CreateReq{Slug: "gh", URL: "https://elsewhere.com"})
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
	ctx := boot(t)
	_, err := Resolve.Call(ctx, ResolveReq{Slug: "nope"})
	if !tako.IsNotFound(err) {
		t.Fatalf("err = %v, want IsNotFound", err)
	}
}

func TestValidation(t *testing.T) {
	ctx := boot(t)
	_, err := create(ctx, CreateReq{Slug: "", URL: ""})
	he, ok := err.(interface{ HTTPStatus() int })
	if !ok || he.HTTPStatus() != 422 {
		t.Fatalf("err = %v, want 422", err)
	}
}
