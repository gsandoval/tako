package tako

import (
	"fmt"

	"github.com/gsandoval/tako/internal/secrets"
)

// Secret declares a secret value resolved once at startup — from .env
// locally, Secret Manager in the cloud. The name must be a string
// constant (analyzer-enforced). Boot refuses to start a service with any
// declared secret unset, which is what lets Value never fail at request
// time.
func Secret(name string) *SecretHandle {
	registerSecret(name)
	return &SecretHandle{name: name}
}

// SecretHandle is a declared secret.
type SecretHandle struct {
	name string
}

// Value returns the secret's value. It takes no Ctx and cannot fail at
// request time: resolution happened at startup. Because there is no Ctx,
// secrets are process-wide — the one backend tests share across apps.
func (s *SecretHandle) Value() string {
	v, ok := secrets.Lookup(s.name)
	if !ok {
		panic(fmt.Sprintf("tako: secret %q is not set — add it to .env (tako dev, generated services) or call app.SetSecret (tests)", s.name))
	}
	return v
}
