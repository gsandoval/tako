package tako

// AB10a: roles you can enforce.
//
// A role is a name an identity carries into the request, and the only
// thing tako knows about it. Who has it is Identity Platform's answer in
// the cloud and the local store's on a laptop; what it MEANS is the
// app's. These pin the seam between those.

import (
	"strings"
	"testing"
)

var (
	testEditor    = Role("editor")
	testModerator = Role("moderator")
	testDesk      = RequireRole(testEditor, testModerator)
)

func TestARoleIsItsName(t *testing.T) {
	if got := testEditor.Name(); got != "editor" {
		t.Fatalf("Name() = %q", got)
	}
}

func TestAUserCarriesTheRolesItArrivedWith(t *testing.T) {
	u := User{Email: "ana@example.com", Roles: []string{"editor"}}
	if !u.HasRole(testEditor) {
		t.Error("HasRole says an editor is not one")
	}
	if u.HasRole(testModerator) {
		t.Error("HasRole invented a role the user does not have")
	}
	// The zero user has nothing, and asking must not panic.
	if (User{}).HasRole(testEditor) {
		t.Error("a user with no roles has one")
	}
}

// RequireRole is any-of, because that is what a door with two keys is.
func TestRequireRoleAdmitsAnyOfThem(t *testing.T) {
	app := NewTestApp(t)
	for _, c := range []struct {
		roles []string
		admit bool
	}{
		{[]string{"editor"}, true},
		{[]string{"moderator"}, true},
		{[]string{"moderator", "editor"}, true},
		{[]string{"reader"}, false},
		{nil, false},
	} {
		u := User{ID: "u", Email: "u@example.test", Roles: c.roles}
		err := testDesk.Check(app.Ctx(u), u)
		if admitted := err == nil; admitted != c.admit {
			t.Errorf("roles %v: admitted = %v, want %v (%v)", c.roles, admitted, c.admit, err)
		}
	}
}

// The refusal says which role would have worked. A role name is not a
// secret, and "not authorized" is the message that sends someone to ask
// a question nobody can answer.
func TestTheRefusalNamesTheRoleThatWouldWork(t *testing.T) {
	app := NewTestApp(t)
	u := User{ID: "u", Email: "u@example.test"}

	err := RequireRole(testEditor).Check(app.Ctx(u), u)
	if !IsDenied(err) {
		t.Fatalf("err = %v, want a denial", err)
	}
	if got := err.Error(); !strings.Contains(got, "editor") {
		t.Errorf("refusal = %q, want it to name the role", got)
	}

	err = testDesk.Check(app.Ctx(u), u)
	for _, want := range []string{"editor", "moderator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %q, missing %q", err.Error(), want)
		}
	}
}

// A test says who somebody is the same way it says their name.
func TestSignInTakesRoles(t *testing.T) {
	app := NewTestApp(t)

	plain := app.SignIn("alice")
	if len(plain.Roles) != 0 {
		t.Errorf("a plain SignIn carries roles: %v", plain.Roles)
	}

	editor := app.SignIn("bob", testEditor)
	if !editor.HasRole(testEditor) {
		t.Errorf("SignIn(name, Editor) did not carry it: %v", editor.Roles)
	}
	if err := testDesk.Check(app.Ctx(editor), editor); err != nil {
		t.Errorf("a signed-in editor was refused: %v", err)
	}
}
