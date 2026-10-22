package backends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Users is the storage contract behind auth.Users(): the accounts an app
// can create and the roles they carry.
//
// The two implementations are not the same kind of thing, and the
// contract is where that is reconciled. In the cloud an account lives in
// Identity Platform and a role is a custom claim on it; on a laptop
// there is no Identity Platform, so both live in the local store. The
// conformance suite is what keeps them answering the same way — a role
// granted must be visible to the next read on either.
type Users interface {
	// Create makes an account. ErrEmailTaken when the address already
	// has one: taking over an existing identity is never implicit.
	Create(ctx context.Context, spec NewUserSpec) (UserRecord, error)
	// Get returns the account by its id, or ErrNotFound.
	Get(ctx context.Context, id string) (UserRecord, error)
	// ByEmail returns the account with this address, or ErrNotFound.
	ByEmail(ctx context.Context, email string) (UserRecord, error)
	// List returns one page of accounts and the cursor to resume from;
	// an empty returned cursor means the last page.
	List(ctx context.Context, limit int, after string) ([]UserRecord, string, error)
	// SetRoles replaces the account's roles wholesale.
	//
	// Wholesale rather than add/remove because that is what Identity
	// Platform offers — setCustomUserClaims replaces the whole claims
	// blob — and a contract that promised a finer operation would be
	// lying about one of its backends. Grant and Revoke are built on
	// top of this, under a lease, in one place.
	SetRoles(ctx context.Context, id string, roles []string) error
	// SetDisabled turns sign-in off or on for the account. The record
	// survives: a disabled user is refused, not forgotten.
	SetDisabled(ctx context.Context, id string, disabled bool) error
	// Delete removes the account. Deleting an absent id is not an error.
	Delete(ctx context.Context, id string) error
}

// NewUserSpec is an account to create. Password may be empty — an
// account that signs in with Google has none, and demanding one would
// make the common case invent a secret nobody uses.
type NewUserSpec struct {
	ID            string
	Email         string
	Password      string
	Name          string
	Roles         []string
	EmailVerified bool
}

// UserRecord is one account as a backend holds it. There is no password
// here in either direction: a hash is the local store's business and
// Identity Platform's, and nothing above this line has a use for one.
type UserRecord struct {
	ID            string
	Email         string
	Name          string
	Roles         []string
	Disabled      bool
	EmailVerified bool
}

// ErrEmailTaken is Create's refusal when the address already has an
// account. It is its own error because the remedy is different from
// every other failure: look the account up and grant it what it needs,
// rather than retrying a create that will never succeed.
var ErrEmailTaken = errors.New("that email already has an account")

// ErrRolesTooLarge is the 1000-byte custom-claims ceiling, refused
// before the write rather than surfaced as Identity Platform's rejection
// — the app can only act on the first version.
var ErrRolesTooLarge = errors.New("roles do not fit Identity Platform's 1000-byte custom-claims limit")

// UsersLimit is the page size Users.List uses when a caller asks for
// none, and the ceiling Identity Platform enforces on batchGet.
const (
	UsersLimitDefault = 50
	UsersLimitMax     = 1000
)

// CheckRolesSize refuses a role set that cannot be stored before the
// write is attempted.
//
// Identity Platform caps custom claims at 1000 bytes of JSON and rejects
// anything larger. Both backends check here so the refusal is the same
// sentence on a laptop and in the cloud — and so the message is about
// the app's mistake rather than a relayed API error the app cannot act
// on.
func CheckRolesSize(roles []string) error {
	raw, err := json.Marshal(map[string][]string{"roles": roles})
	if err != nil {
		return err
	}
	if len(raw) > 1000 {
		return fmt.Errorf("%w (%d roles, %d bytes) — a role is a name, not a payload",
			ErrRolesTooLarge, len(roles), len(raw))
	}
	return nil
}
