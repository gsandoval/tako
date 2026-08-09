// Package routes is the ONE definition of every URL tako mounts on the
// user's behalf.
//
// It exists for the reason [internal/metrics] exists: these paths are
// agreed on by parts of the system that never call each other. Codegen
// writes the mount into a generated main; the runtime's local signer
// builds URLs that point back at one of them; the provisioner targets
// them from Cloud Scheduler, Cloud Tasks and a Pub/Sub push
// subscription; alert queries match on them as a metric label; the
// analyzer refuses to let a user declare one. Six spellings of the same
// string, and until FL5 every one of them was a literal typed out again.
//
// # The namespace is reserved
//
// Everything here lives under [Prefix]. That prefix is tako's, wholly:
// the generated service answers it before the user's router is
// consulted, so nothing under it can be declared, shadowed or reordered
// by an app — static files in a site directory included. The reservation
// is a claim on the URL space, not a race that the framework happens to
// win.
//
// It is also what makes the mounts safe to register at all. Go's
// ServeMux refuses to build a router holding two patterns where neither
// is more specific on both path and method, and a framework route
// mounted beside a user's wildcard page is exactly that pair:
//
//	panic: pattern "/api/_session" conflicts with pattern "GET /{section}/{slug}"
//
// That was a startup crash for any app pairing tako.Auth with a
// two-segment wildcard GET route — a CMS, a docs site, a shop with
// /{category}/{product} — and mounting these on a mux of their own is
// what makes it unrepresentable rather than merely avoided. Nesting one
// mux inside another does NOT work: to the parent, a mounted sub-router
// is just another pattern, and it panics the same way.
//
// # The rooms
//
//	/_tako/api/…    machine-facing: JSON, tokens, form posts, OIDC triggers
//	/_tako/z/…      human-facing diagnostics (reserved, nothing serves it yet)
//	/_tako/<page>   the framework's own pages — SignIn today, few ever
//
// A room is for a family of endpoints; a page is a page, and lives at
// the root. These names are a CONTRACT in the same sense the metric
// names are: they appear in deployed Cloud Scheduler jobs, in push
// subscriptions, in alert queries, and in any sign-in code a user wrote
// against them. testdata/lock.golden.txt makes an edit announce itself
// at the moment it happens rather than at the moment a deploy stops
// delivering.
package routes

import (
	"strings"
)

// Prefix is the reserved namespace. Every path below begins with it.
//
// The trailing slash is deliberate: it is a prefix test, not a segment,
// and [Reserved] pairs it with an exact match on the bare "/_tako" so a
// request that stops at the namespace root is answered by tako rather
// than falling through to the app.
const Prefix = "/_tako/"

// Bare is Prefix without its trailing slash — the namespace root, which
// is reserved too and serves 404.
const Bare = "/_tako"

// The rooms.
const (
	// API is everything a program talks to: JSON bodies, bearer-ish
	// tokens in a path, form posts from tako's own dev bar, and the
	// OIDC-verified triggers Google's schedulers send.
	API = Prefix + "api/"
	// Z is human-facing diagnostics. Reserved and empty: reserving it
	// later is the same breaking change as this one, and a namespace
	// that only becomes reserved once something needs it was never
	// reserved. Whatever lands here answers to auth first — a page
	// describing a running service is not public.
	Z = Prefix + "z/"
)

// The mounts, in the order a lock file lists them.
const (
	// Session exchanges an Identity Platform ID token for a session
	// (POST) and revokes one (DELETE). The handler answers both and 405s
	// everything else, which it can only do because it owns the pattern
	// rather than sharing a mux with the app.
	Session = API + "session"
	// SignOut is the form-post counterpart to DELETE Session: an HTML
	// form cannot issue a DELETE, and the dev bar is an HTML form.
	SignOut = Session + "/signout"
	// Push is where a worker receives its deliveries — a Pub/Sub push
	// subscription for topics, a Cloud Tasks target for queues, both
	// OIDC-verified and private.
	Push = API + "push"
	// Signed is the subtree local signed URLs point back at; the token
	// is the rest of the path and is the whole authorization.
	Signed = API + "signed/"
	// Cron is Cloud Scheduler's trigger. One mount serves every cron in
	// the service, named by the {cron} wildcard.
	Cron = API + "cron/"
	// SignIn is the framework-owned sign-in page — the one thing here a
	// person looks at rather than a program posts to, which is why it
	// sits at the namespace root and not in API.
	SignIn = Prefix + "signin"
	// DevReady is how `tako dev` establishes that the port it just
	// probed is answered by the child IT started, rather than by
	// whatever else was already listening. The response is a per-run
	// nonce the dev loop puts in the child's environment.
	//
	// It is not a mount: no mux registers it, and a deployed service
	// answers it 404 like anything else under Z, because the nonce is
	// only ever set by `tako dev`. It is listed in the lock all the same
	// — the point of the lock is that a new URL is reviewed, and a URL
	// that answers under some condition is still a URL.
	DevReady = Z + "dev"
)

// The mux patterns codegen writes, method and all. They are spelled here
// rather than assembled at the call site so that the lock file covers
// the METHOD as well as the path: mounting Session without one is what
// FL5 was about.
const (
	PatternSession = Session
	PatternSignOut = "POST " + SignOut
	PatternPush    = "POST " + Push
	PatternSigned  = Signed
	PatternCron    = "POST " + Cron + "{cron}"
	PatternSignIn  = "GET " + SignIn
)

// CronRoute is the metric label and Scheduler path for one named cron.
// The mux pattern alone is POST /_tako/api/cron/{cron}, which would
// merge every cron in a service into one series and leave no alert able
// to tell them apart.
func CronRoute(name string) string { return Cron + name }

// SignedToken extracts the token from a signed-URL request path.
func SignedToken(path string) string { return strings.TrimPrefix(path, Signed) }

// IsReserved reports whether a request path belongs to tako rather than
// to the app. The bare namespace root counts: /_tako with no trailing
// slash is not the app's either.
func IsReserved(path string) bool {
	return path == Bare || strings.HasPrefix(path, Prefix)
}

// All is every mount, in lock order. The lock file is generated from it,
// so a new mount that skips this slice is a new mount nobody reviewed.
var All = []struct {
	Name    string
	Pattern string
}{
	{"Session", PatternSession},
	{"SignOut", PatternSignOut},
	{"Push", PatternPush},
	{"Signed", PatternSigned},
	{"Cron", PatternCron},
	{"SignIn", PatternSignIn},
}

// Lock renders the mount table for testdata/lock.golden.txt.
func Lock() string {
	var b strings.Builder
	b.WriteString("# tako's reserved URL surface. Every line is a contract:\n")
	b.WriteString("# deployed Scheduler jobs, push subscriptions, alert queries and\n")
	b.WriteString("# user sign-in code are all written against these strings.\n")
	b.WriteString("prefix\t" + Prefix + "\n")
	b.WriteString("room\t" + API + "\n")
	b.WriteString("room\t" + Z + "\n")
	for _, m := range All {
		b.WriteString(m.Name + "\t" + m.Pattern + "\n")
	}
	// Not a mount, and marked as one thing it is not: this answers only
	// under `tako dev`. It is here so that adding it was reviewed.
	b.WriteString("dev-only\tGET " + DevReady + "\n")
	return b.String()
}
