package conformance

// The accounts chapter.
//
// This is the chapter with the widest gap between its two
// implementations: local keeps accounts in bbolt, gcp keeps them in
// Identity Platform, and a role is a row in one and a custom claim in
// the other. Everything below is behaviour an app can rely on without
// knowing which it is talking to — and nothing below is behaviour only
// one of them could have.

import (
	"context"
	"errors"
	"testing"

	"github.com/gsandoval/tako/pkg/backends"
)

// RunUsers runs the accounts conformance suite against one backend.
func RunUsers(t *testing.T, open func(t *testing.T) backends.Users) {
	t.Run("CreateAndRead", func(t *testing.T) {
		s, ctx := open(t), context.Background()
		made, err := s.Create(ctx, backends.NewUserSpec{
			Email: "ana@example.com", Name: "Ana", Password: "correct horse battery",
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if made.ID == "" {
			t.Fatal("Create returned no id")
		}
		if made.Email != "ana@example.com" {
			t.Errorf("email = %q", made.Email)
		}

		got, err := s.Get(ctx, made.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ID != made.ID || got.Email != made.Email {
			t.Errorf("Get = %+v, want %+v", got, made)
		}

		byEmail, err := s.ByEmail(ctx, "ana@example.com")
		if err != nil {
			t.Fatalf("ByEmail: %v", err)
		}
		if byEmail.ID != made.ID {
			t.Errorf("ByEmail id = %q, want %q", byEmail.ID, made.ID)
		}
	})

	// An address is one account. Taking over an existing identity is
	// never something a create does by accident, and the refusal is its
	// own error because the remedy is different: look it up and grant.
	t.Run("EmailIsTakenOnce", func(t *testing.T) {
		s, ctx := open(t), context.Background()
		if _, err := s.Create(ctx, backends.NewUserSpec{Email: "dup@example.com"}); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		_, err := s.Create(ctx, backends.NewUserSpec{Email: "dup@example.com"})
		if !errors.Is(err, backends.ErrEmailTaken) {
			t.Fatalf("second Create err = %v, want ErrEmailTaken", err)
		}
	})

	// Addresses are matched case-insensitively, because Identity
	// Platform lower-cases them. Without this, a duplicate would be
	// refused in the cloud and accepted on a laptop.
	t.Run("EmailIsCaseInsensitive", func(t *testing.T) {
		s, ctx := open(t), context.Background()
		if _, err := s.Create(ctx, backends.NewUserSpec{Email: "Mixed@Example.com"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := s.ByEmail(ctx, "mixed@example.com"); err != nil {
			t.Errorf("ByEmail(lower) after Create(Mixed): %v", err)
		}
		if _, err := s.Create(ctx, backends.NewUserSpec{Email: "MIXED@EXAMPLE.COM"}); !errors.Is(err, backends.ErrEmailTaken) {
			t.Errorf("a differently-cased duplicate was accepted: %v", err)
		}
	})

	t.Run("MissingIsNotFound", func(t *testing.T) {
		s, ctx := open(t), context.Background()
		if _, err := s.Get(ctx, "nobody"); !errors.Is(err, backends.ErrNotFound) {
			t.Errorf("Get(missing) = %v, want ErrNotFound", err)
		}
		if _, err := s.ByEmail(ctx, "nobody@example.com"); !errors.Is(err, backends.ErrNotFound) {
			t.Errorf("ByEmail(missing) = %v, want ErrNotFound", err)
		}
	})

	// The claim this whole slice exists for: a role set is visible to
	// the next read, on either backend.
	t.Run("RolesRoundTrip", func(t *testing.T) {
		s, ctx := open(t), context.Background()
		u, err := s.Create(ctx, backends.NewUserSpec{Email: "roles@example.com", Roles: []string{"editor"}})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got, _ := s.Get(ctx, u.ID); !sameRoles(got.Roles, []string{"editor"}) {
			t.Errorf("roles at create = %v", got.Roles)
		}
		if err := s.SetRoles(ctx, u.ID, []string{"editor", "moderator"}); err != nil {
			t.Fatalf("SetRoles: %v", err)
		}
		got, err := s.Get(ctx, u.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !sameRoles(got.Roles, []string{"editor", "moderator"}) {
			t.Errorf("roles = %v, want [editor moderator]", got.Roles)
		}
		// And taking them all away is a state, not a no-op.
		if err := s.SetRoles(ctx, u.ID, nil); err != nil {
			t.Fatalf("SetRoles(nil): %v", err)
		}
		if got, _ := s.Get(ctx, u.ID); len(got.Roles) != 0 {
			t.Errorf("roles after clearing = %v", got.Roles)
		}
	})

	// The 1000-byte ceiling is Identity Platform's, and both backends
	// refuse it identically — an app that only ran locally would
	// otherwise meet it for the first time in production.
	t.Run("RolesHaveACeiling", func(t *testing.T) {
		s, ctx := open(t), context.Background()
		u, err := s.Create(ctx, backends.NewUserSpec{Email: "big@example.com"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		huge := make([]string, 120)
		for i := range huge {
			huge[i] = "role-with-a-long-name"
		}
		if err := s.SetRoles(ctx, u.ID, huge); !errors.Is(err, backends.ErrRolesTooLarge) {
			t.Fatalf("SetRoles(huge) = %v, want ErrRolesTooLarge", err)
		}
	})

	t.Run("DisableKeepsTheRecord", func(t *testing.T) {
		s, ctx := open(t), context.Background()
		u, err := s.Create(ctx, backends.NewUserSpec{Email: "off@example.com"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := s.SetDisabled(ctx, u.ID, true); err != nil {
			t.Fatalf("SetDisabled: %v", err)
		}
		got, err := s.Get(ctx, u.ID)
		if err != nil {
			t.Fatalf("Get after disable: %v", err)
		}
		if !got.Disabled {
			t.Error("the account is not disabled")
		}
		if err := s.SetDisabled(ctx, u.ID, false); err != nil {
			t.Fatalf("SetDisabled(false): %v", err)
		}
		if got, _ := s.Get(ctx, u.ID); got.Disabled {
			t.Error("the account stayed disabled")
		}
	})

	t.Run("DeleteRemoves", func(t *testing.T) {
		s, ctx := open(t), context.Background()
		u, err := s.Create(ctx, backends.NewUserSpec{Email: "gone@example.com"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := s.Delete(ctx, u.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := s.Get(ctx, u.ID); !errors.Is(err, backends.ErrNotFound) {
			t.Errorf("Get after Delete = %v, want ErrNotFound", err)
		}
		// Deleting what is not there is not a failure.
		if err := s.Delete(ctx, u.ID); err != nil {
			t.Errorf("second Delete: %v", err)
		}
		// And the address is free again.
		if _, err := s.Create(ctx, backends.NewUserSpec{Email: "gone@example.com"}); err != nil {
			t.Errorf("Create after Delete: %v", err)
		}
	})

	t.Run("ListPages", func(t *testing.T) {
		s, ctx := open(t), context.Background()
		const total = 7
		for i := range total {
			if _, err := s.Create(ctx, backends.NewUserSpec{Email: string(rune('a'+i)) + "@example.com"}); err != nil {
				t.Fatalf("Create %d: %v", i, err)
			}
		}
		seen := map[string]bool{}
		cursor, pages := "", 0
		for {
			page, next, err := s.List(ctx, 3, cursor)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			for _, u := range page {
				if seen[u.ID] {
					t.Errorf("%s appeared on two pages", u.ID)
				}
				seen[u.ID] = true
			}
			pages++
			if next == "" || pages > 10 {
				break
			}
			cursor = next
		}
		if len(seen) != total {
			t.Errorf("paged through %d accounts, want %d", len(seen), total)
		}
	})
}

func sameRoles(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
