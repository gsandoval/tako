package tako

// Auth declares the app's authentication: which sign-in providers exist
// (WithGoogle, WithEmail) and, implicitly, the session endpoint the
// framework injects. Handlers opt into requiring a user with WithAuth.
func Auth(opts ...Option) *AuthHandle {
	return &AuthHandle{opts: opts}
}

// AuthHandle is the app's declared authentication configuration.
type AuthHandle struct {
	opts []Option
}
