package main

import (
	"strings"
	"testing"
)

func TestValidEnvNamesPass(t *testing.T) {
	for _, env := range []string{"prod", "staging", "dev", "pr-42", "a", "e2e-run-7"} {
		if err := checkEnv(env); err != nil {
			t.Errorf("checkEnv(%q) = %v, want nil", env, err)
		}
	}
}

// The env is typed by a person and lands in every resource name and
// every label, so it is refused rather than derived: silently turning
// --env Staging into "staging" would make tako destroy --env Staging
// address something the user never named.
func TestIllegalEnvNamesAreRefusedWithTheirRepair(t *testing.T) {
	cases := []struct {
		env      string
		wantHint string
	}{
		{"Staging", "did you mean --env staging?"},
		{"feature/x", "did you mean --env feature-x?"},
		{"PR_42", "did you mean --env pr-42?"},
	}
	for _, c := range cases {
		err := checkEnv(c.env)
		if err == nil {
			t.Fatalf("checkEnv(%q) = nil, want an error", c.env)
		}
		got := err.Error()
		if !strings.HasPrefix(got, `--env "`+c.env+`" is not a valid environment name`) {
			t.Errorf("checkEnv(%q) first line = %q", c.env, strings.SplitN(got, "\n", 2)[0])
		}
		if !strings.Contains(got, "lowercase letters, digits and hyphens") {
			t.Errorf("checkEnv(%q) does not say what is allowed:\n%s", c.env, got)
		}
		if !strings.Contains(got, c.wantHint) {
			t.Errorf("checkEnv(%q) missing %q:\n%s", c.env, c.wantHint, got)
		}
	}
}

// No repair to offer, so none is offered — a hint that repeats the
// rejected value would be worse than silence.
func TestEnvNamesWithNoRepairGetNoHint(t *testing.T) {
	for _, env := range []string{"", "...", "---"} {
		err := checkEnv(env)
		if err == nil {
			t.Fatalf("checkEnv(%q) = nil, want an error", env)
		}
		if strings.Contains(err.Error(), "did you mean") {
			t.Errorf("checkEnv(%q) offered a repair:\n%s", env, err)
		}
	}
}

// An env past the bound is refused rather than truncated: truncation
// would let two envs share one set of resource names.
func TestOverlongEnvIsRefused(t *testing.T) {
	if err := checkEnv(strings.Repeat("e", 21)); err == nil {
		t.Error("a 21-character env was accepted")
	}
}

// parseTarget is the one place every cloud command reads --env, so the
// check belongs there and not in each command.
func TestParseTargetChecksTheEnv(t *testing.T) {
	if _, _, err := parseTarget([]string{"--project", "p", "--env", "Staging"}); err == nil {
		t.Error("parseTarget accepted --env Staging")
	}
	if _, _, err := parseTarget([]string{"--project", "p", "--env", "pr-42"}); err != nil {
		t.Errorf("parseTarget rejected --env pr-42: %v", err)
	}
}
