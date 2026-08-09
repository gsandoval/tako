package tako

import (
	"context"
	"errors"
	"fmt"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// staleSessions and endSessions reach the runtime's session store
// through the boot seam: the SDK changes a role, the runtime owns what a
// session is. Both are no-ops in a bare test, where nothing has minted
// one.
func staleSessions(ctx Ctx, uid string) error {
	if hooks.StaleSessions == nil {
		return nil
	}
	return hooks.StaleSessions(ctx, uid)
}

func endSessions(ctx Ctx, uid string) error {
	if hooks.EndSessions == nil {
		return nil
	}
	return hooks.EndSessions(ctx, uid)
}

// Accounts: the people who may sign in, and what they may do.
//
// tako does not decide who is an editor — that stays the app's, and it
// is why tako.Role knows a name and nothing else. What tako owns is the
// mechanism: an account exists, it carries roles, and a role change
// takes effect. In the cloud that is Identity Platform (accounts) and a
// custom claim (roles); on a laptop it is the local store. The
// conformance suite is the contract between them.
//
// Recovery is deliberately NOT here. Password reset and email
// verification are Identity Platform's hosted flows, because they are an
// outbound-email story and tako sends no email.

// Users is the account surface, reached from the app's declared auth.
//
//	var auth = tako.Auth(tako.AuthConfig{Google: true, Password: true})
//
//	users := auth.Users()
//	u, err := users.Create(ctx, tako.NewUser{Email: "ana@example.com", Roles: []string{Editor.Name()}})
//	err = users.Grant(ctx, u.ID, Moderator)
//
// The grant this needs is DERIVED: a service whose package only reads
// (Get, ByEmail, List) is granted roles/identitytoolkit.viewer, one that
// writes gets roles/identitytoolkit.admin, and one that never calls this
// gets neither. "This service can mint accounts" stays a property of the
// service that asked.
func (a *AuthHandle) Users() *UsersHandle { return &UsersHandle{} }

// UsersHandle is the account store behind tako.Auth.
type UsersHandle struct{}

// NewUser is an account to create.
//
// Password may be empty, and usually is: an account that signs in with
// Google has none, and demanding one would make the common case invent a
// secret nobody uses. Where it is set, Identity Platform holds it in the
// cloud and the local store holds a PBKDF2 verifier on a laptop — tako
// never keeps a password.
type NewUser struct {
	Email    string
	Password string
	Name     string
	Roles    []string
}

// Create makes an account and returns it.
//
// An address is one account: creating over an existing one is refused
// with an error tako.IsEmailTaken recognises, because the remedy is to
// look it up and grant it what it needs rather than to retry.
func (h *UsersHandle) Create(ctx Ctx, spec NewUser) (User, error) {
	store, err := usersBackend()
	if err != nil {
		return User{}, err
	}
	rec, err := store.Create(ctx, backends.NewUserSpec{
		Email: spec.Email, Password: spec.Password, Name: spec.Name, Roles: spec.Roles,
	})
	if err != nil {
		return User{}, mapUserErr(err)
	}
	return userFromRecord(rec), nil
}

// Get returns the account with this id; tako.IsNotFound reports absence.
func (h *UsersHandle) Get(ctx Ctx, id string) (User, error) {
	store, err := usersBackend()
	if err != nil {
		return User{}, err
	}
	rec, err := store.Get(ctx, id)
	if err != nil {
		return User{}, mapUserErr(err)
	}
	return userFromRecord(rec), nil
}

// ByEmail returns the account with this address.
func (h *UsersHandle) ByEmail(ctx Ctx, email string) (User, error) {
	store, err := usersBackend()
	if err != nil {
		return User{}, err
	}
	rec, err := store.ByEmail(ctx, email)
	if err != nil {
		return User{}, mapUserErr(err)
	}
	return userFromRecord(rec), nil
}

// List pages through the accounts.
//
//	page, err := users.List(ctx).Limit(50).Page("")
//	for _, u := range page.Users { … }
//	// page.Next is the cursor for the next call, empty at the end.
func (h *UsersHandle) List(ctx Ctx) *UserQuery { return &UserQuery{ctx: ctx} }

// UserQuery is a page request over the accounts.
type UserQuery struct {
	ctx   Ctx
	limit int
}

// Limit sets the page size.
func (q *UserQuery) Limit(n int) *UserQuery { q.limit = n; return q }

// Page returns one page, resuming after the cursor (empty starts at the
// beginning).
func (q *UserQuery) Page(cursor string) (UsersPage, error) {
	store, err := usersBackend()
	if err != nil {
		return UsersPage{}, err
	}
	recs, next, err := store.List(q.ctx, q.limit, cursor)
	if err != nil {
		return UsersPage{}, mapUserErr(err)
	}
	page := UsersPage{Next: next}
	for _, rec := range recs {
		page.Users = append(page.Users, userFromRecord(rec))
	}
	return page, nil
}

// UsersPage is one page of accounts and the cursor to resume from. An
// empty Next means this was the last page.
type UsersPage struct {
	Users []User
	Next  string
}

// Grant adds a role to an account.
//
// Revoking and granting are read-modify-write, because a role lives in a
// claims blob that is written whole — so both run under a lease keyed on
// the account. Without it, two grants at the same moment are a lost
// update, and the one that lost is a person who is quietly missing a
// role nobody will think to check.
func (h *UsersHandle) Grant(ctx Ctx, id string, role RoleHandle) error {
	return h.changeRoles(ctx, id, func(have []string) []string {
		for _, r := range have {
			if r == role.name {
				return have
			}
		}
		return append(have, role.name)
	})
}

// Revoke removes a role from an account.
func (h *UsersHandle) Revoke(ctx Ctx, id string, role RoleHandle) error {
	return h.changeRoles(ctx, id, func(have []string) []string {
		out := have[:0]
		for _, r := range have {
			if r != role.name {
				out = append(out, r)
			}
		}
		return out
	})
}

func (h *UsersHandle) changeRoles(ctx Ctx, id string, apply func([]string) []string) error {
	store, err := usersBackend()
	if err != nil {
		return err
	}
	change := func(c Ctx) error {
		rec, err := store.Get(c, id)
		if err != nil {
			return mapUserErr(err)
		}
		next := apply(append([]string(nil), rec.Roles...))
		if sameStrings(rec.Roles, next) {
			return nil // nothing changed; no write, no session churn
		}
		if err := store.SetRoles(c, id, next); err != nil {
			return mapUserErr(err)
		}
		// The roles a request sees come off the SESSION, which was
		// minted from a token issued before this change. Marking is what
		// makes the change land on the next request instead of at the
		// next sign-in — and it costs one write per live session rather
		// than a read per request.
		return staleSessions(c, id)
	}
	if hooks.Lease == nil {
		return change(ctx) // unwired (bare test): no concurrency to guard
	}
	ran, err := backends.TryDoLease(ctx, hooks.Lease, "users-"+id, hooks.LeaseOwner, backends.LeaseTTL,
		func(c context.Context) error { return change(reroot(ctx, c)) })
	if err != nil {
		return err
	}
	if !ran {
		return fmt.Errorf("tako: another role change for %s is in flight — retry", id)
	}
	return nil
}

// Disable turns sign-in off for an account without deleting it, and ends
// the sessions it already has: a disabled user who stays signed in for a
// week is not disabled.
func (h *UsersHandle) Disable(ctx Ctx, id string) error { return h.setDisabled(ctx, id, true) }

// Enable turns sign-in back on.
func (h *UsersHandle) Enable(ctx Ctx, id string) error { return h.setDisabled(ctx, id, false) }

func (h *UsersHandle) setDisabled(ctx Ctx, id string, disabled bool) error {
	store, err := usersBackend()
	if err != nil {
		return err
	}
	if err := store.SetDisabled(ctx, id, disabled); err != nil {
		return mapUserErr(err)
	}
	if disabled {
		return endSessions(ctx, id)
	}
	return nil
}

// Delete removes an account and ends its sessions. Deleting an absent id
// is not an error.
func (h *UsersHandle) Delete(ctx Ctx, id string) error {
	store, err := usersBackend()
	if err != nil {
		return err
	}
	if err := store.Delete(ctx, id); err != nil {
		return mapUserErr(err)
	}
	return endSessions(ctx, id)
}

// IsEmailTaken reports whether err is Create's refusal of an address that
// already has an account.
func IsEmailTaken(err error) bool { return errors.Is(err, backends.ErrEmailTaken) }

func userFromRecord(rec backends.UserRecord) User {
	return User{
		ID: rec.ID, Email: rec.Email, Name: rec.Name,
		Roles: rec.Roles, EmailVerified: rec.EmailVerified,
	}
}

func mapUserErr(err error) error {
	if errors.Is(err, backends.ErrNotFound) {
		return NotFound()
	}
	return err
}

func usersBackend() (backends.Users, error) {
	if hooks.Users == nil {
		return nil, errors.New("tako: accounts are not wired — auth.Users() works in generated services (tako dev / deploy) and under NewTestApp, not in a bare test")
	}
	return hooks.Users, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
