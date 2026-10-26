package tako_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/runtime"
)

func TestTestAppSignInUpsertsUserRecordAndEmailVerified(t *testing.T) {
	app := tako.NewTestApp(t)
	editor := tako.Role("editor")
	u := app.SignIn("alice", editor)
	if !u.EmailVerified {
		t.Error("app.SignIn user EmailVerified = false, want true")
	}

	users := tako.Auth(tako.AuthConfig{Google: true}).Users()
	got, err := users.Get(app.Ctx(), u.ID)
	if err != nil {
		t.Fatalf("users.Get(%q) after app.SignIn: %v", u.ID, err)
	}
	if got.Email != "alice@example.test" || !got.EmailVerified {
		t.Errorf("stored user = %+v", got)
	}
	if len(got.Roles) != 1 || got.Roles[0] != "editor" {
		t.Errorf("stored roles = %v, want [editor]", got.Roles)
	}
}

func TestParallelTestAppsIsolateUsersAndLocks(t *testing.T) {
	app1 := tako.NewTestApp(t)
	app2 := tako.NewTestApp(t)

	users := tako.Auth(tako.AuthConfig{Password: true}).Users()
	if _, err := users.Create(app1.Ctx(), tako.NewUser{Email: "only-in-app1@example.com"}); err != nil {
		t.Fatalf("app1 Create: %v", err)
	}

	page, err := users.List(app2.Ctx()).Page("")
	if err != nil {
		t.Fatalf("app2 List: %v", err)
	}
	if len(page.Users) != 0 {
		t.Fatalf("app2 saw %d users from app1, want 0", len(page.Users))
	}

	lock := tako.Lock("shared-name")
	entered1 := make(chan struct{})
	release1 := make(chan struct{})
	done1 := make(chan error, 1)
	go func() {
		done1 <- lock.Do(app1.Ctx(), func(ctx tako.Ctx) error {
			close(entered1)
			<-release1
			return nil
		})
	}()
	<-entered1

	ran2, err := lock.TryDo(app2.Ctx(), func(ctx tako.Ctx) error { return nil })
	close(release1)
	<-done1
	if err != nil {
		t.Fatalf("app2 TryDo: %v", err)
	}
	if !ran2 {
		t.Fatal("app2 lock contended with app1's lock — Lease backend was not scoped per TestApp")
	}
}

type isoItem struct {
	ID    string `json:"id" entity:"id"`
	Owner string `json:"owner"`
}

type isoWriteReq struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

type isoStateRes struct {
	User  string    `json:"user"`
	Items []isoItem `json:"items"`
	Disk  string    `json:"disk"`
}

var (
	isoItems = tako.Entity[isoItem]("iso-items")
	isoDisk  = tako.Disk("iso-disk")

	isoWrite = tako.API("POST /iso/items", func(ctx tako.Ctx, req isoWriteReq) (isoStateRes, error) {
		if err := isoItems.Put(ctx, isoItem{ID: req.ID, Owner: ctx.User().Name}); err != nil {
			return isoStateRes{}, err
		}
		if err := isoDisk.WriteFile(ctx, "note.txt", []byte(req.Note), 0o644); err != nil {
			return isoStateRes{}, err
		}
		return isoStateRes{User: ctx.User().Name}, nil
	})

	isoRead = tako.API("GET /iso/items", func(ctx tako.Ctx, _ struct{}) (isoStateRes, error) {
		items, err := isoItems.Query(ctx).All()
		if err != nil {
			return isoStateRes{}, err
		}
		raw, _ := isoDisk.ReadFile(ctx, "note.txt")
		return isoStateRes{
			User:  ctx.User().Name,
			Items: items,
			Disk:  string(raw),
		}, nil
	})
)

func TestParallelTestAppHandlersIsolateSessionsEntitiesAndDisks(t *testing.T) {
	prevMux := hooks.TestMux
	t.Cleanup(func() { hooks.TestMux = prevMux })

	runtime.RegisterTestMux(func() http.Handler {
		takoMux := http.NewServeMux()
		takoMux.Handle("/_tako/api/session", runtime.SessionHandler("google"))
		takoMux.Handle("POST /_tako/api/session/signout", runtime.SignOutHandler())

		mux := http.NewServeMux()
		mux.Handle("POST /iso/items", runtime.JSONHandler(isoWrite, func(r *http.Request) (isoWriteReq, error) {
			var req isoWriteReq
			err := json.NewDecoder(r.Body).Decode(&req)
			return req, err
		}, runtime.RequireAuth()))
		mux.Handle("GET /iso/items", runtime.JSONHandler(isoRead, nil, runtime.RequireAuth()))
		return runtime.Reserved(takoMux, mux)
	})

	app1 := tako.NewTestApp(t)
	app2 := tako.NewTestApp(t)
	h1 := app1.Handler()
	h2 := app2.Handler()

	signInHTTP := func(h http.Handler, devUser string) *http.Cookie {
		t.Helper()
		form := url.Values{"dev_user": {devUser}}
		req := httptest.NewRequest(http.MethodPost, "/_tako/api/session", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		for _, c := range rec.Result().Cookies() {
			if c.Name == "_session" && c.Value != "" {
				return c
			}
		}
		t.Fatalf("sign-in as %q did not set _session cookie (status %d: %s)", devUser, rec.Code, rec.Body.String())
		return nil
	}

	cookie1 := signInHTTP(h1, "alice")
	cookie2 := signInHTTP(h2, "bob")

	// Session minted on app1 must not authenticate on app2.
	crossReq := httptest.NewRequest(http.MethodGet, "/iso/items", nil)
	crossReq.AddCookie(cookie1)
	crossRec := httptest.NewRecorder()
	h2.ServeHTTP(crossRec, crossReq)
	if crossRec.Code != http.StatusUnauthorized {
		t.Fatalf("app1 session cookie accepted by app2: status %d, want 401", crossRec.Code)
	}

	// Write distinct entity and disk state through each app's HTTP handler.
	postItem := func(h http.Handler, c *http.Cookie, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/iso/items", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /iso/items failed (%d): %s", rec.Code, rec.Body.String())
		}
	}
	postItem(h1, cookie1, `{"id":"item-1","note":"disk-app1"}`)
	postItem(h2, cookie2, `{"id":"item-2","note":"disk-app2"}`)

	getState := func(h http.Handler, c *http.Cookie) isoStateRes {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/iso/items", nil)
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /iso/items failed (%d): %s", rec.Code, rec.Body.String())
		}
		var out isoStateRes
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return out
	}

	st1 := getState(h1, cookie1)
	if st1.User != "alice" || len(st1.Items) != 1 || st1.Items[0].ID != "item-1" || st1.Disk != "disk-app1" {
		t.Fatalf("app1 state = %+v, want user=alice items=[item-1] disk=disk-app1", st1)
	}

	st2 := getState(h2, cookie2)
	if st2.User != "bob" || len(st2.Items) != 1 || st2.Items[0].ID != "item-2" || st2.Disk != "disk-app2" {
		t.Fatalf("app2 state = %+v, want user=bob items=[item-2] disk=disk-app2", st2)
	}
}
