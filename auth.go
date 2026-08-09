package tako

import "github.com/gsandoval/tako/internal/hooks"

// Auth declares the app's authentication: which sign-in providers exist
// (AuthConfig.Google, AuthConfig.Password) and, implicitly, the session endpoint the
// framework injects. Routes opt into requiring a user with Route{SignIn: true}.
func Auth(cfg AuthConfig) *AuthHandle {
	return &AuthHandle{
		cfg:         cfg,
		Registered:  AuthEvent{name: authRegisteredSource},
		FirstSignIn: AuthEvent{name: authFirstSignInSource},
	}
}

// AuthHandle is the app's declared authentication configuration.
type AuthHandle struct {
	// Registered fires when this app created an account through
	// auth.Users().Create. Bind it with tako.Subscribe.
	Registered AuthEvent
	// FirstSignIn fires the first time tako mints a session for an
	// identity it has never seen — whoever created the account, and
	// however they signed in.
	//
	// It is the wider of the two: a Google sign-up, a self-service
	// registration through Identity Platform's own SDK, and an account
	// somebody made in the console all reach an app through here. What
	// it cannot see is an account created and NEVER used, which is the
	// honest limit of observing sign-ins rather than registrations.
	//
	// For a Google sign-up the two events fire within a second of each
	// other, so a handler bound to both must be idempotent.
	FirstSignIn AuthEvent

	cfg AuthConfig
}

// The two moments tako can honestly announce about a person.
//
// There is deliberately no "somebody registered with Identity Platform"
// event, because a Cloud-Run-only framework cannot observe one:
// beforeCreate blocking functions and Firebase Auth create triggers are
// both **Gen1 Cloud Functions only**, and tako provisions no functions.
// Everything below is something tako itself does, and can therefore
// state without guessing.

// Registered fires when THIS APP created the account, through
// auth.Users().Create. Bind a handler with
// tako.Subscribe(auth.Registered, fn); the payload is the new tako.User.
//
// It does not fire for an account created in the Identity Platform
// console, or by somebody signing up through Identity Platform's own
// client SDK — tako did not do those and does not hear about them.
// FirstSignIn is what covers those.

// AuthEvent is one of the two account event sources on AuthHandle,
// Registered and FirstSignIn. Its payload type is tako.User.
type AuthEvent struct{ name string }

func (AuthEvent) eventSource(User) {}

func (e AuthEvent) sourceName() string { return e.name }

// The source names are topic names on the wire, and they live in
// internal/hooks because pkg/runtime publishes FirstSignIn and this
// package publishes Registered — one constant, not two that must agree.
const (
	authRegisteredSource  = hooks.AuthRegisteredSource
	authFirstSignInSource = hooks.AuthFirstSignInSource
)
