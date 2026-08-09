package tako

// AB4: an app can ask where it is running.
//
// The finding these pin: flauta authorized on os.Getenv("TAKO_BACKEND"),
// tako's private boot switch, at five call sites — one of them the rule
// that admits dev identities to its admin. So the cases below are about
// an authorization input, not a convenience.

import "testing"

func TestEnvironmentIsLocalUntilADeploySaysOtherwise(t *testing.T) {
	// No TAKO_BACKEND: go test, tako test, a bare binary.
	t.Setenv("TAKO_BACKEND", "")
	if got := Environment(); got != EnvLocal {
		t.Fatalf("Environment() = %v, want EnvLocal", got)
	}
}

func TestEnvironmentIsCloudWhenDeployed(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "gcp")
	if got := Environment(); got != EnvCloud {
		t.Fatalf("Environment() = %v, want EnvCloud", got)
	}
}

// An unknown value is EnvLocal and not a third state. Two reasons: the
// enum is a promise about what an app may switch on, and a typo must
// never be the thing that turns a local affordance on in production —
// which is the direction that costs something.
func TestAnUnknownBackendIsLocalRatherThanAThirdState(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "gcpp")
	if got := Environment(); got != EnvLocal {
		t.Fatalf("Environment() = %v, want EnvLocal", got)
	}
}

func TestEnvPrintsAsItIsWritten(t *testing.T) {
	for _, c := range []struct {
		env  Env
		want string
	}{
		{EnvLocal, "local"},
		{EnvCloud, "cloud"},
		{Env(7), "Env(7)"},
	} {
		if got := c.env.String(); got != c.want {
			t.Errorf("Env(%d).String() = %q, want %q", c.env, got, c.want)
		}
	}
}
