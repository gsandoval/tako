package main

import (
	"fmt"
	"strings"
)

// maxEnv bounds an environment name. Envs prefix every resource name
// (gcp.Prefixed), so the bound is what keeps a bucket or a service
// name from being illegal because of the env alone.
const maxEnv = 20

// checkEnv validates --env.
//
// The app identifier is DERIVED when Google will not take it
// (gcp.AppID) because it comes from go.mod and the user cannot
// reasonably change it. An env is the opposite: a person typed it on
// the command line. Deriving "staging" from "Staging" would make
// `tako destroy --env Staging` address a set of objects the user never
// named, and the destroy path is the one where a silent
// reinterpretation is unforgivable. So it is refused, with the repair
// spelled out.
func checkEnv(env string) error {
	if legalEnv(env) {
		return nil
	}
	msg := fmt.Sprintf("--env %q is not a valid environment name\n\n"+
		"    environments become part of every resource name and every label,\n"+
		"    so they are limited to lowercase letters, digits and hyphens\n"+
		"    (1–%d characters), starting with a letter", env, maxEnv)
	if fixed := repairEnv(env); fixed != "" && fixed != env {
		msg += fmt.Sprintf("\n\n    did you mean --env %s?", fixed)
	}
	return fmt.Errorf("%s", msg)
}

// legalEnv is the rule the message above describes.
func legalEnv(env string) bool {
	if env == "" || len(env) > maxEnv {
		return false
	}
	if c := env[0]; c < 'a' || c > 'z' {
		return false
	}
	for _, r := range env {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// repairEnv is the suggestion, and "" when there is nothing to
// suggest — a hint that just echoes the rejected value is noise.
func repairEnv(env string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(env) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	fixed := strings.Trim(b.String(), "-")
	if !legalEnv(fixed) {
		return ""
	}
	return fixed
}
