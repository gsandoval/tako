package tako

import "testing"

// NewTestApp returns an app instance backed entirely by fresh in-memory
// backends — no cloud credentials, no network. Handlers are invoked
// directly (Endpoint.Call), workers and subscribers run synchronously via
// DrainQueues, and everything is cleaned up when the test ends.
func NewTestApp(tb testing.TB) *TestApp {
	panic("tako: not implemented")
}

// TestApp is an in-memory instance of the app under test.
type TestApp struct {
	tb testing.TB
}

// SignIn returns a stub identity for name, creating it on first use.
func (a *TestApp) SignIn(name string) User {
	panic("tako: not implemented")
}

// Ctx returns a request context, authenticated as the given user if one is
// passed.
func (a *TestApp) Ctx(as ...User) Ctx {
	panic("tako: not implemented")
}

// DrainQueues synchronously runs every worker and subscriber until all
// published messages are consumed.
func (a *TestApp) DrainQueues() {
	panic("tako: not implemented")
}

// AdvanceCron runs the named cron handler once, as if its schedule fired.
func (a *TestApp) AdvanceCron(name string) {
	panic("tako: not implemented")
}

// Chaos enables chaos mode on the app's queues and topics: random
// redelivery, reordering, and duplicate delivery, seeded and reproducible,
// so at-least-once bugs surface locally instead of in production.
func (a *TestApp) Chaos() {
	panic("tako: not implemented")
}
