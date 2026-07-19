package tako

// Secret declares a secret value resolved once at startup. The name must be
// a string constant (analyzer-enforced).
func Secret(name string, opts ...Option) *SecretHandle {
	return &SecretHandle{name: name, opts: opts}
}

// SecretHandle is a declared secret.
type SecretHandle struct {
	name string
	opts []Option
}

// Value returns the secret's value. It takes no Ctx and cannot fail at
// request time: resolution happened at startup.
func (s *SecretHandle) Value() string {
	panic("tako: not implemented")
}
