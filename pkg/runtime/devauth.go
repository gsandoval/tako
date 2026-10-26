package runtime

import (
	"context"
	"errors"
	"slices"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/pkg/backends"
)

// signInRefused is the exact text — product UX, pinned by tests. It
// says nothing about WHICH half was wrong.
const signInRefused = "that email and password do not match an account"

// passwordSignIn verifies a local account's password. Declared providers
// still apply: an app that never declared Password has no such door,
// exactly as in the cloud, where the same declaration decides which
// tokens tako will accept.
func passwordSignIn(ctx context.Context, email, password string) (tako.User, error) {
	if !slices.Contains(declaredProviders, "email") {
		return tako.User{}, errors.New("this app does not declare tako.AuthConfig{Password: true}")
	}
	verifier, ok := usersStore(ctx).(interface {
		VerifyPassword(context.Context, string, string) (backends.UserRecord, error)
	})
	if !ok {
		return tako.User{}, errors.New("this backend cannot verify passwords")
	}
	rec, err := verifier.VerifyPassword(ctx, email, password)
	if err != nil {
		return tako.User{}, err
	}
	if rec.Disabled {
		// A disabled account is refused with the same sentence: whether
		// an address exists is not something a sign-in page tells you.
		return tako.User{}, errors.New("account disabled")
	}
	return tako.User{
		ID: rec.ID, Email: rec.Email, Name: rec.Name,
		Provider: "email", EmailVerified: rec.EmailVerified, Roles: rec.Roles,
	}, nil
}

func defaultDevProvider(override string) string {
	if override != "" {
		return override
	}
	if slices.Contains(declaredProviders, "google") {
		return "google"
	}
	if slices.Contains(declaredProviders, "github") {
		return "github"
	}
	if len(declaredProviders) > 0 {
		return declaredProviders[0]
	}
	return "email"
}

func toggleRoleSlice(roles []string, role string) []string {
	var out []string
	found := false
	for _, r := range roles {
		if r == role {
			found = true
			continue
		}
		out = append(out, r)
	}
	if !found {
		out = append(out, role)
	}
	return out
}

// devAccount is what `tako dev` signs in as: a real account in the local
// store, created on first sight and updated with whatever roles were
// ticked. The ticks ARE the local grant — there is no console to open.
func devAccount(ctx context.Context, name string, roles []string, explicitRoles bool, toggleRole, providerOverride string) (tako.User, error) {
	email := name + "@example.test"
	provider := defaultDevProvider(providerOverride)
	if !explicitRoles && toggleRole != "" {
		roles = toggleRoleSlice(nil, toggleRole)
	}
	base := tako.User{
		ID: name, Email: email, Name: name,
		Provider: provider, EmailVerified: true, Roles: roles,
	}
	users := usersStore(ctx)
	if users == nil {
		return base, nil // unwired: the session still works, it just has no record
	}
	rec, err := users.ByEmail(ctx, email)
	if errors.Is(err, backends.ErrNotFound) {
		rec, err = users.Create(ctx, backends.NewUserSpec{
			Email: email, Name: name, Roles: roles, EmailVerified: true,
		})
	} else if err == nil {
		nextRoles := rec.Roles
		shouldUpdate := false
		if explicitRoles {
			nextRoles = roles
			shouldUpdate = !sameRoles(rec.Roles, nextRoles)
		} else if toggleRole != "" {
			nextRoles = toggleRoleSlice(rec.Roles, toggleRole)
			shouldUpdate = true
		}
		if shouldUpdate {
			if err = users.SetRoles(ctx, rec.ID, nextRoles); err == nil {
				rec.Roles = nextRoles
			}
		}
	}
	if err != nil {
		return tako.User{}, err
	}
	return tako.User{
		ID: rec.ID, Email: rec.Email, Name: rec.Name,
		Provider: provider, EmailVerified: true, Roles: rec.Roles,
	}, nil
}

func sameRoles(a, b []string) bool {
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
