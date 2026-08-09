package tako

import (
	"os"
	"strconv"
)

// Env is where this process is running.
//
// It exists because apps were reading TAKO_BACKEND — tako's private boot
// switch — to find out. That is a string tako never promised, and the
// first app to read it used the answer to decide who may reach its
// admin: dev identities minted by `tako dev` are admitted locally and
// must not be deployed. An authorization input has to be surface.
type Env int

const (
	// EnvLocal is `tako dev`, `tako test`, `go test`, and a binary run
	// by hand: the in-process backends, and whatever a developer
	// needs to get at them.
	EnvLocal Env = iota
	// EnvCloud is a service deployed by `tako deploy`.
	EnvCloud
)

// String renders the environment as it is written in the docs.
func (e Env) String() string {
	switch e {
	case EnvLocal:
		return "local"
	case EnvCloud:
		return "cloud"
	}
	return "Env(" + strconv.Itoa(int(e)) + ")"
}

// Environment reports where this process is running.
//
//	if tako.Environment() == tako.EnvLocal {
//		v.DevUsers = devIdentities   // the local sign-in strip
//	}
//
// It is a fact about the deployment, never a feature flag. Use it to
// decide what a DEVELOPMENT affordance may do — a seeded sign-in, a
// visible fixture, an extra log line — and not to change what the
// product is: an app that branches its behaviour on this has two
// products and tests one.
//
// A package-level function rather than a method on Ctx because the
// answer is not per-request: a rule like "these identities are
// admitted locally" wants to stay pure and testable without a session,
// and a constructor may want to ask before any request exists.
//
// Anything other than a recognised deployment is EnvLocal, so a
// mistyped environment can only ever turn a local affordance OFF in
// production, which is the safe direction for the branch this answers.
func Environment() Env {
	if os.Getenv("TAKO_BACKEND") == "gcp" {
		return EnvCloud
	}
	return EnvLocal
}
