package tako_test

import (
	"testing"

	"github.com/gsandoval/tako"
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
