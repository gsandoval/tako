package docserver

// The docs name FIELDS now, and nothing checked them.
//
// TestEveryTakoSymbolTheDocsNameExists checks `tako.X` — the type-level
// claim. TestTheDocsDoNotNameRetiredWithOptions checks that the retired
// `WithX` vocabulary stays gone. Neither looks at what replaced it:
// after that retirement the docs spell options as `Route.SignIn`,
// `CronConfig.Singleflight`, `SiteConfig.Build` — 100+ of them across
// the guide and reference — and a renamed or deleted field would rot
// exactly the way `WithDomain` did, silently, reading like
// documentation the whole time.
//
// That is the gap discovered work #5 named as "the check misses bare
// symbol names". The bare-name form worth checking is not any
// capitalised word — it is a QUALIFIED member, because that is a claim
// about API precise enough to verify and specific enough not to
// false-positive on prose.

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/gsandoval/tako/docs"
)

// member matches Type.Member: two capitalised identifiers, no space.
// A `tako.Route.SignIn` matches its tail, which is the same claim.
var member = regexp.MustCompile(`\b([A-Z][A-Za-z0-9]*)\.([A-Z][A-Za-z0-9]*)\b`)

// lockMembers reads the SDK lock into type → its fields and methods.
// The lock is the source of truth for what exists, the same one the api
// page is generated from.
func lockMembers(t *testing.T) map[string]map[string]bool {
	t.Helper()
	raw, err := fs.ReadFile(docs.FS, "api/surface.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]bool{}
	add := func(typ, name string) {
		if out[typ] == nil {
			out[typ] = map[string]bool{}
		}
		out[typ][name] = true
	}
	methodRe := regexp.MustCompile(`^\s*func \(\*?([A-Za-z0-9_]+)(?:\[[^\]]*\])?\)\.([A-Za-z0-9_]+)`)
	structRe := regexp.MustCompile(`^type ([A-Za-z0-9_]+)(?:\[[^\]]*\])? struct\{(.*)\}$`)
	for _, line := range strings.Split(string(raw), "\n") {
		if m := methodRe.FindStringSubmatch(line); m != nil {
			add(m[1], m[2])
			continue
		}
		if m := structRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			for _, field := range strings.Split(m[2], ";") {
				field = strings.TrimSpace(field)
				if i := strings.IndexByte(field, ' '); i > 0 {
					add(m[1], field[:i])
				}
			}
			// A struct with no exported fields still exists.
			if out[m[1]] == nil {
				out[m[1]] = map[string]bool{}
			}
		}
	}
	return out
}

func TestEveryFieldTheDocsNameExists(t *testing.T) {
	members := lockMembers(t)
	// Names that look like Type.Member and are not: Go packages, other
	// people's APIs, and file names.
	notOurs := map[string]bool{
		"Cloud.Run": true, "Cloud.Logging": true, "Identity.Platform": true,
	}
	var checked int
	for _, section := range []string{SectionGuide, SectionReference} {
		files, err := fs.Glob(docs.FS, section+"/*.md")
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			raw, err := fs.ReadFile(docs.FS, file)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(raw), "\n") {
				for _, m := range member.FindAllStringSubmatch(line, -1) {
					typ, name := m[1], m[2]
					fields, known := members[typ]
					if !known || notOurs[typ+"."+name] {
						continue // not a tako type; prose, or someone else's
					}
					checked++
					if !fields[name] {
						t.Errorf("%s:%d: the docs name %s.%s, and %s has no such field or method",
							file, i+1, typ, name, typ)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Type.Field mentions were checked — the scanner found nothing, which means it is broken, not that the docs are clean")
	}
	t.Logf("checked %d Type.Member mentions against the lock", checked)
}

// The scanner has to be able to fail. A check that cannot produce a
// finding is a check nobody can trust.
func TestTheMemberScannerCatchesAMissingField(t *testing.T) {
	members := lockMembers(t)
	fields, ok := members["Route"]
	if !ok {
		t.Fatal("Route is not in the lock")
	}
	if !fields["SignIn"] {
		t.Error("Route.SignIn should be known")
	}
	if fields["Cache"] {
		t.Error("Route.Cache does not exist and should not be known")
	}
}
