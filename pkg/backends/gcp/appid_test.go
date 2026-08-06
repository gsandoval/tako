package gcp

import "strings"
import "testing"

// The examples' module paths. Every one of these must be its own id:
// they are what tako has deployed with since C1, and the label IS the
// state, so a derivation that moved them would orphan every object of
// every app already in a project.
var exampleModules = []string{
	"blog", "gallery", "hello", "journal", "shop", "shortlink", "siren",
}

func TestAppIDLeavesLegalModulesAlone(t *testing.T) {
	for _, m := range exampleModules {
		if got := AppID(m); got != m {
			t.Errorf("AppID(%q) = %q, want it unchanged", m, got)
		}
	}
}

func TestAppIDDerivesFromHostShapedPaths(t *testing.T) {
	cases := []struct {
		module string
		want   string
	}{
		// flauta's, the module that found this.
		{"github.com/gsandoval/flauta", "flauta-a70a87"},
		// The example this ships with.
		{"github.com/gsandoval/tako-example-hello", "tako-example-hello-812a9c"},
		// Uppercase and underscores are legal in a module path and not
		// in a label; a dot inside the last segment is the usual /v2.
		{"github.com/Me/My_App.v2", "my_app-v2-6d176d"},
		// Nothing salvageable in the last segment: the hash carries it
		// alone rather than producing a name that starts with a hyphen.
		{"example.com/...", "app-2b26bd"},
		// A trailing slash leaves an empty last segment.
		{"example.com/pkg/", "app-616a3a"},
	}
	for _, c := range cases {
		if got := AppID(c.module); got != c.want {
			t.Errorf("AppID(%q) = %q, want %q", c.module, got, c.want)
		}
	}
}

// A bare module name too long for a bucket segment is derived like any
// other illegal one — legality is not only about characters.
func TestAppIDShortensAnOverlongBareName(t *testing.T) {
	long := "a-very-long-bare-module-name-past-thirty"
	got := AppID(long)
	if got == long {
		t.Fatalf("AppID(%q) returned it unchanged (%d chars)", long, len(long))
	}
	if len(got) > 29 {
		t.Errorf("AppID(%q) = %q, %d chars — too long to leave room in a bucket name", long, got, len(got))
	}
	if !strings.HasPrefix(got, "a-very-long-bare-modu") {
		t.Errorf("AppID(%q) = %q, want the readable head kept", long, got)
	}
}

// The id is what Google is asked to store, so it must satisfy the
// rules that made this task exist: GCS/Run/Pub-Sub label values and
// the app segment of a bucket name.
func TestEveryDerivedIDIsLegal(t *testing.T) {
	modules := append([]string{
		"github.com/gsandoval/flauta",
		"github.com/Me/My_App.v2",
		"example.com/...",
		"example.com/pkg/",
		"a-very-long-bare-module-name-past-thirty",
		"UPPER",
		"9leading",
		"-leading-hyphen",
	}, exampleModules...)
	for _, m := range modules {
		id := AppID(m)
		if !legalAppID(id) {
			t.Errorf("AppID(%q) = %q, which is not a legal id", m, id)
		}
	}
}

// Idempotence is what lets the id be stored and re-derived: a deploy
// writes it into TAKO_APP, and the runtime hands that value straight
// to BucketName. If AppID(AppID(m)) drifted, the names the services
// read would stop being the names deploys create.
func TestAppIDIsIdempotent(t *testing.T) {
	for _, m := range []string{
		"github.com/gsandoval/flauta", "hello", "example.com/...",
		"a-very-long-bare-module-name-past-thirty",
	} {
		once := AppID(m)
		if twice := AppID(once); twice != once {
			t.Errorf("AppID(AppID(%q)): %q → %q", m, once, twice)
		}
	}
}

// Two modules sharing a last segment must not share an id: the label
// is the filter `tako destroy` keys on, so a collision would let one
// app destroy another's objects.
func TestAppIDSeparatesModulesSharingALastSegment(t *testing.T) {
	a := AppID("github.com/gsandoval/flauta")
	b := AppID("gitlab.com/someone-else/flauta")
	if a == b {
		t.Errorf("both modules derived %q", a)
	}
}

func TestAppIDIsStableAcrossCalls(t *testing.T) {
	const m = "github.com/gsandoval/flauta"
	if AppID(m) != AppID(m) {
		t.Error("AppID is not a pure function of its input")
	}
}
