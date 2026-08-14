package docserver

// reference/errors.md calls itself the index of every refusal tako makes
// at analysis time, and says "the text is golden-tested, so what you
// read below is what you will see". Nothing held it to either claim.
//
// It was wrong about both. Eighteen rule ids — every role, policy and
// alert rule, plus resource-name, route-conflict and reserved-prefix —
// fired in a real app and appeared nowhere on the page; and six of the
// texts it quoted had no fixture behind them, so they were transcribed
// by hand and could drift silently. One of them was: two quoted lines
// rendered a traffic setting as "http2" and a service as "X", neither of
// which any tako build has ever printed.
//
// The two tests below are the claims, one each.
//
// Where the id list comes from: `Rule: "…"` literals in the analyzer's
// sources, plus every id the golden corpus actually emitted. The second
// half is what catches a rule whose id reaches Error.Rule through a
// variable (alert-subject does). A brand-new rule with neither a literal
// nor a fixture would slip both — which is a rule with no test at all,
// already refused by the project's test-first rule for analyzer work.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gsandoval/tako/docs"
)

var (
	ruleLiteral = regexp.MustCompile(`Rule:\s*"([a-z0-9-]+)"`)
	ruleInGold  = regexp.MustCompile(`\(tako/([a-z0-9-]+)\)`)
	ruleRow     = regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)` \\|")
	goldenLine  = regexp.MustCompile(`^[^:]+:\d+:\d+: (.*) \(tako/[a-z0-9-]+\)$`)
	quotedLine  = regexp.MustCompile(`^(?:[^:]+:\d+:\d+: )?(?:⚠ )?(.*?)(?: \(tako/[a-z0-9-]+\))?$`)
)

// analyzerRuleIDs is every rule id the analyzer can attach to an error.
func analyzerRuleIDs(t *testing.T) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	sources, err := filepath.Glob(filepath.Join(repoRoot, "pkg/analyzer/*.go"))
	if err != nil || len(sources) == 0 {
		t.Fatalf("globbing the analyzer sources: %v", err)
	}
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ruleLiteral.FindAllSubmatch(src, -1) {
			ids[string(m[1])] = true
		}
	}
	for _, golden := range goldenFiles(t) {
		for _, m := range ruleInGold.FindAllSubmatch(golden, -1) {
			ids[string(m[1])] = true
		}
	}
	return ids
}

func goldenFiles(t *testing.T) [][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repoRoot, "pkg/analyzer/testdata/errors/*/expect.golden.txt"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("globbing the golden corpus: %v", err)
	}
	var out [][]byte
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

func errorsPage(t *testing.T) string {
	t.Helper()
	src, err := fs.ReadFile(docs.FS, "reference/errors.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

func TestEveryAnalyzerRuleIsInTheErrorsIndex(t *testing.T) {
	page := errorsPage(t)
	documented := map[string]bool{}
	for _, m := range ruleRow.FindAllStringSubmatch(page, -1) {
		documented[m[1]] = true
	}
	var undocumented, phantom []string
	for id := range analyzerRuleIDs(t) {
		if !documented[id] {
			undocumented = append(undocumented, id)
		}
	}
	for id := range documented {
		if !analyzerRuleIDs(t)[id] {
			phantom = append(phantom, id)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(phantom)
	if len(undocumented) > 0 {
		t.Errorf("%d rules can fire in an app and are in no table in reference/errors.md: %s\n"+
			"The page is the index of every analysis error; a rule missing from it is one a\n"+
			"reader meets for the first time in their terminal.", len(undocumented),
			strings.Join(undocumented, ", "))
	}
	if len(phantom) > 0 {
		t.Errorf("reference/errors.md documents %d rules the analyzer cannot emit: %s\n"+
			"A rule that was removed has to leave the page too, or it sends readers looking\n"+
			"for a refusal that will never come.", len(phantom), strings.Join(phantom, ", "))
	}
}

// notInTheCorpus are the quoted lines that legitimately have no golden
// behind them, each with the reason. Anything else quoted on the page has
// to be a message the corpus actually produced — that is what makes
// "the text is golden-tested" true rather than aspirational.
var notInTheCorpus = map[string]string{
	// A Go type error, not an analysis error: tako.Route has no Footprint
	// field, so the compiler answers before the analyzer runs. A fixture
	// for it could not load, which is the point the page is making.
	"unknown field Footprint in struct literal of type tako.Route": "a compiler error, quoted as one",

	// A warning, and TestErrors only collects refusals. Its text is
	// pinned in pkg/analyzer/shadow_test.go instead.
	"GET /{year}/{slug} shadows 3 files the site serves": "a warning; pinned by shadow_test.go",
}

func TestEveryErrorTextTheIndexQuotesIsGoldenTested(t *testing.T) {
	corpus := map[string]bool{}
	for _, golden := range goldenFiles(t) {
		for _, line := range strings.Split(string(golden), "\n") {
			if m := goldenLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				corpus[m[1]] = true
			}
		}
	}

	page := errorsPage(t)
	var quoted []string
	// Splitting on the fence marker alternates outside/inside, so the odd
	// pieces are the blocks. A block's first line is the language tag when
	// it has no spaces in it.
	for i, block := range strings.Split(page, "```") {
		if i%2 == 0 {
			continue
		}
		for j, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || (j == 0 && !strings.Contains(line, " ")) {
				continue
			}
			quoted = append(quoted, line)
		}
	}
	if len(quoted) < 50 {
		t.Fatalf("found only %d quoted lines — the extraction stopped working, not the page", len(quoted))
	}

	checked := 0
	for _, line := range quoted {
		msg := line
		if m := quotedLine.FindStringSubmatch(line); m != nil {
			msg = m[1]
		}
		if corpus[msg] {
			checked++
			continue
		}
		if explained(msg) {
			continue
		}
		t.Errorf("reference/errors.md quotes a message the golden corpus never produced:\n"+
			"  %s\n"+
			"Either it drifted from the real text, or the rule has no fixture. Add the case to\n"+
			"pkg/analyzer/testdata/errors/ and paste the golden's line, or — if it genuinely\n"+
			"cannot have one — say why in notInTheCorpus.", msg)
	}
	t.Logf("%d of %d quoted lines matched a golden byte for byte", checked, len(quoted))
}

func explained(msg string) bool {
	for prefix := range notInTheCorpus {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}

// The other family of texts the docs quote verbatim: the panic a
// resource raises when no backend is wired. Ten copies across eight
// reference pages, and DOC1's checker reaches none of them — it covers
// analyzer errors and CLI commands, both of which live somewhere a test
// can enumerate. These live in the SDK, so changing one silently rots
// every page that quotes it (FL26 changed all ten at once, which is
// what prompted this).
//
// The docs wrap them across lines, so both sides are compared with
// whitespace collapsed.
var unwiredQuote = regexp.MustCompile(`tako: no (\w+) backend wired`)

func TestTheDocsQuoteTheRealUnwiredPanic(t *testing.T) {
	// The one true sentence, read from the SDK rather than repeated here.
	src, err := os.ReadFile(filepath.Join(repoRoot, "appctx.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?s)func unwiredPanic\(backend string\) string \{(.*?)\n\}`).FindSubmatch(src)
	if body == nil {
		t.Fatal("unwiredPanic is gone or reshaped — this test reads its body to learn the text")
	}
	// The text is built as "…" + backend + "…", so each side is read on
	// its own and its string literals joined — the concatenation is what
	// makes one sentence out of eight callers.
	left, right, found := strings.Cut(string(body[1]), "+ backend +")
	if !found {
		t.Fatal("unwiredPanic no longer interpolates the backend name")
	}
	prefix, suffix := literals(left), literals(right)
	if prefix == "" || suffix == "" {
		t.Fatalf("could not read the message halves: %q / %q", prefix, suffix)
	}

	pages, err := filepath.Glob(filepath.Join(repoRoot, "docs/reference/*.md"))
	if err != nil {
		t.Fatal(err)
	}
	quoted := 0
	for _, page := range pages {
		raw, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		// The whole page, collapsed — the docs wrap these across two and
		// three lines, and where the wrap falls is not the point.
		flat := collapse(string(raw))
		for _, m := range unwiredQuote.FindAllStringSubmatchIndex(flat, -1) {
			quoted++
			want := collapse(prefix + flat[m[2]:m[3]] + suffix)
			if !strings.HasPrefix(flat[m[0]:], want) {
				end := min(m[0]+len(want)+20, len(flat))
				t.Errorf("%s quotes a panic the SDK does not raise:\n  got:  %s\n  want: %s",
					filepath.Base(page), flat[m[0]:end], want)
			}
		}
	}
	if quoted < 8 {
		t.Errorf("found only %d quoted panics — the extraction stopped working, not the docs", quoted)
	}
	t.Logf("%d quoted copies across the reference, all matching the SDK", quoted)
}

// collapse makes a line-wrapped markdown quote comparable with a Go
// string: runs of whitespace become one space.
func collapse(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "`", "")), " ")
}

// literals joins the Go string literals in a fragment of source, which
// is what a multi-line concatenated message looks like on the page.
func literals(src string) string {
	var b strings.Builder
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(src, -1) {
		b.WriteString(m[1])
	}
	return b.String()
}
