package tako

// Auth declares the app's authentication: which sign-in providers exist
// (AuthConfig.Google, AuthConfig.Password) and, implicitly, the session endpoint the
// framework injects. Routes opt into requiring a user with Route{SignIn: true}.
func Auth(cfg AuthConfig) *AuthHandle {
	return &AuthHandle{cfg: cfg}
}

// AuthHandle is the app's declared authentication configuration.
type AuthHandle struct {
	cfg AuthConfig
}
