package docserver

// The docs name the SDK constantly — `tako.Entity`, `tako.Window{For: …}`,
// `tako.NewTestApp`. Nothing checked that the things they name exist.
//
// The link check (TestEveryLinkBetweenDocsResolves) catches a page that
// points at a page that is gone. It cannot catch a page that names a
// symbol which is gone, and that is the worse failure: a broken link is
// obviously broken, while `tako.Absent` reads like documentation. Three
// pages carried exactly that when this test was written.
//
// **What it deliberately does not check: the `WithX` vocabulary.** The
// tech spec, the API reviews and the reference all talk about
// `WithProfile`, `WithSingleflight`, `WithAuth` — 59 mentions across the
// reference alone — and tako exports no `With` anything; options are
// fields on config structs. Those are the project's names for option
// FAMILIES, not claims about Go symbols, which is why the pattern here
// requires the `tako.` qualifier: `tako.X` asserts "this is API", bare
// `WithX` does not.
//
// The limit that leaves is real and worth stating. cache.md said
// "`WithTTL` is the only Cache option" when Cache has no options at all
// — the TTL is a required second parameter. No symbol check catches a
// sentence that is wrong about a symbol that exists.
//
// The scanner lives here rather than in the package because nothing
// serves it. It checks the docs; it should not ride in every tako binary.

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/gsandoval/tako/docs"
)

// Exported Go identifiers start with a capital, which is what keeps
// label names like `tako.app/tako.service` out of the sweep.
var takoMention = regexp.MustCompile(`tako\.([A-Z][A-Za-z0-9_]*)`)

// A mention is one `tako.X` written in the docs.
type mention struct {
	File string
	Line int
	Name string
}

func mentions(file, src string) []mention {
	var out []mention
	for i, line := range strings.Split(src, "\n") {
		for _, m := range takoMention.FindAllStringSubmatch(line, -1) {
			out = append(out, mention{File: file, Line: i + 1, Name: m[1]})
		}
	}
	return out
}

func TestEveryTakoSymbolTheDocsNameExists(t *testing.T) {
	site := built(t)

	// A mention is good if the lock has it, as a declaration or as a
	// method. Methods count because the docs legitimately write
	// `tako.Percentile` when they mean the reading method, and an error
	// text tako itself emits does the same.
	known := map[string]bool{}
	for _, s := range site.Symbols {
		known[s.Name] = true
		for _, m := range s.Methods {
			known[m.Name] = true
		}
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
			for _, m := range mentions(path.Base(file), string(raw)) {
				checked++
				if !known[m.Name] {
					t.Errorf("%s:%d: the docs name tako.%s, and tako exports no such symbol",
						file, m.Line, m.Name)
				}
			}
		}
	}
	t.Logf("checked %d tako.X mentions across the guide and the reference", checked)
	if checked == 0 {
		t.Fatal("no mentions were found — the scanner is not reaching the docs")
	}
}

func TestMentionsIgnoresWhatIsNotASymbol(t *testing.T) {
	got := mentions("x.md", strings.Join([]string{
		"Declare `tako.Entity[Post](\"posts\")` and query it.",
		"The images carry tako.app/tako.service labels.",
		"A `tako.Window{For: time.Minute}` sustains.",
		"Chain it: tako.Requests(x).Status(\"5xx\").Fraction()",
	}, "\n"))
	want := []mention{
		{"x.md", 1, "Entity"},
		{"x.md", 3, "Window"},
		{"x.md", 4, "Requests"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mention %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The retired `WithX` vocabulary stays retired.
//
// X2 replaced a flat Option namespace (23 `With…` functions) with typed
// config structs, and for a while the docs kept the old names: 84
// mentions across 22 spellings, none of them a symbol a reader could
// write. TestEveryTakoSymbolTheDocsNameExists could not see them,
// deliberately — its pattern requires the `tako.` qualifier, and these
// were bare — so the vocabulary survived the rename that removed it.
// tako's own error texts had drifted the same way: one preflight told
// users to "drop tako.WithDomain", which has never been a symbol.
//
// The docs now name fields (`Route.SignIn`, `CronConfig.Singleflight`,
// `Compute.Profile`), which is both what a user types and what tako's
// analyzer already says when it refuses one. This test is what keeps
// the two spellings from diverging again.
func TestTheDocsDoNotNameRetiredWithOptions(t *testing.T) {
	// Not ours: Google's Jobs API method, named in job.md's account of
	// what the deploy calls.
	notOurs := map[string]bool{"runWithOverrides": true}

	withOption := regexp.MustCompile(`\b(run)?With[A-Z][A-Za-z]*`)

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
				for _, m := range withOption.FindAllString(line, -1) {
					if notOurs[m] {
						continue
					}
					checked++
					t.Errorf("%s:%d: the docs name %s — tako has no With… options; "+
						"name the field a user actually writes (Route.SignIn, CronConfig.Singleflight, Compute.Profile)",
						file, i+1, m)
				}
			}
		}
	}
	if checked > 0 {
		t.Logf("%d retired option name(s) still in the docs", checked)
	}
}
