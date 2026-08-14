package runtime

// FL23: the template set could not be replaced while the process ran.
//
// It was six package vars filled in place — `siteTemplates = map[...]{}`
// and then one assignment per template — so a second registration during
// a render was a concurrent map read and write, which Go answers with
// `fatal error`, not a recoverable panic. The process dies. And a lock
// alone would not have been enough: a reader holding it mid-build sees a
// half-filled registry, some templates present and others "not found".
//
// The shape of the fix is one immutable value published in one atomic
// store, and these tests are about what that buys: no crash, no torn
// read, and a candidate that can be rendered before it is published.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
)

// templateFS builds a site FS whose templates/ holds exactly these
// files, keyed by path relative to templates/.
func templateFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, src := range files {
		fsys["templates/"+name] = &fstest.MapFile{Data: []byte(src)}
	}
	return fsys
}

const shell = `<!doctype html><html><body>{{ block "content" . }}{{ end }}</body></html>`

func liveSite(t *testing.T, files map[string]string) {
	t.Helper()
	RegisterSiteTemplates(templateFS(files), SiteOptions{})
}

// The regression that IS this slice. Before the change this test does
// not fail — it takes the process down with `fatal error: concurrent map
// read and map write`, which is why it is worth having.
func TestRenderingWhileTheSetIsReplacedIsSafe(t *testing.T) {
	liveSite(t, map[string]string{
		"layout.html": shell,
		"home.html":   `{{ define "content" }}v1{{ end }}`,
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// A render must never fail while sets come and go: every
				// set below has home.html, so "not found" here means a
				// reader saw a half-built registry.
				if _, err := renderSite(context.Background(), "home.html", nil); err != nil {
					t.Errorf("render during a swap: %v", err)
					return
				}
			}
		}()
	}
	for i := range 200 {
		set, err := prepareTemplates(templateFS(map[string]string{
			"layout.html": shell,
			"home.html":   `{{ define "content" }}v` + string(rune('0'+i%10)) + `{{ end }}`,
		}))
		if err != nil {
			t.Fatal(err)
		}
		if err := useTemplates(set); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

// A published set replaces what renders, and nothing else.
func TestUseTemplatesPublishesTheCandidate(t *testing.T) {
	liveSite(t, map[string]string{"layout.html": shell, "home.html": `{{ define "content" }}before{{ end }}`})
	out, err := renderSite(context.Background(), "home.html", nil)
	if err != nil || !strings.Contains(string(out), "before") {
		t.Fatalf("baseline render = %q, %v", out, err)
	}

	set, err := prepareTemplates(templateFS(map[string]string{
		"layout.html": shell,
		"home.html":   `{{ define "content" }}after{{ end }}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := useTemplates(set); err != nil {
		t.Fatal(err)
	}
	out, err = renderSite(context.Background(), "home.html", nil)
	if err != nil || !strings.Contains(string(out), "after") {
		t.Fatalf("after publishing = %q, %v", out, err)
	}
}

// All or nothing, and every failure at once: an editor who broke two
// templates should not have to save twice to hear about the second.
func TestABrokenCandidateIsRefusedWholeAndNamesEveryFailure(t *testing.T) {
	liveSite(t, map[string]string{"layout.html": shell, "home.html": `{{ define "content" }}live{{ end }}`})

	_, err := prepareTemplates(templateFS(map[string]string{
		"layout.html": shell,
		"home.html":   `{{ define "content" }}fine{{ end }}`,
		"a.html":      `{{ define "content" }}{{ .Missing `,
		"b.html":      `{{ if }}`,
	}))
	if err == nil {
		t.Fatal("a candidate with two unparseable templates was accepted")
	}
	for _, want := range []string{"a.html", "b.html"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s:\n%v", want, err)
		}
	}
	// And the live set is untouched: the site kept serving throughout.
	out, err := renderSite(context.Background(), "home.html", nil)
	if err != nil || !strings.Contains(string(out), "live") {
		t.Fatalf("a refused candidate disturbed the live set: %q, %v", out, err)
	}
}

// The case the three-part API exists for. This candidate PARSES — Go
// resolves {{ template }} at execute time — and only rendering it finds
// the missing partial. A gate on parsing alone would have published it.
func TestACandidateThatParsesCanStillFailToRender(t *testing.T) {
	liveSite(t, map[string]string{
		"layout.html": shell,
		"card.html":   `<div>card</div>`,
		"home.html":   `{{ define "content" }}{{ template "card.html" . }}{{ end }}`,
	})

	// The editor deleted card.html and kept the page that uses it.
	set, err := prepareTemplates(templateFS(map[string]string{
		"layout.html": shell,
		"home.html":   `{{ define "content" }}{{ template "card.html" . }}{{ end }}`,
	}))
	if err != nil {
		t.Fatalf("the candidate did not parse, so this test proves nothing: %v", err)
	}
	if _, err := renderTemplateSet(context.Background(), set, "home.html", nil); err == nil {
		t.Fatal("rendering a page whose partial is gone succeeded — the candidate check is worthless")
	} else if !strings.Contains(err.Error(), "card.html") {
		t.Errorf("the render failure does not name the missing template: %v", err)
	}
	// Rendering a candidate must not publish it, whatever the outcome.
	out, err := renderSite(context.Background(), "home.html", nil)
	if err != nil || !strings.Contains(string(out), "card") {
		t.Fatalf("rendering a candidate disturbed the live set: %q, %v", out, err)
	}
}

// An in-flight render finishes against the set it started with. The
// candidate is rendered while a newer set is published under it.
func TestAnInFlightRenderKeepsItsOwnSet(t *testing.T) {
	liveSite(t, map[string]string{"layout.html": shell, "home.html": `{{ define "content" }}original{{ end }}`})
	candidate, err := prepareTemplates(templateFS(map[string]string{
		"layout.html": shell,
		"home.html":   `{{ define "content" }}candidate{{ end }}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := prepareTemplates(templateFS(map[string]string{
		"layout.html": shell,
		"home.html":   `{{ define "content" }}replacement{{ end }}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := useTemplates(replacement); err != nil {
		t.Fatal(err)
	}
	out, err := renderTemplateSet(context.Background(), candidate, "home.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "candidate") {
		t.Errorf("a candidate render saw another set: %q", out)
	}
}

func TestUseTemplatesRefusesSomethingItDidNotPrepare(t *testing.T) {
	liveSite(t, map[string]string{"layout.html": shell, "home.html": `{{ define "content" }}x{{ end }}`})
	for _, bad := range []any{nil, "templates/", 42} {
		if err := useTemplates(bad); err == nil {
			t.Errorf("useTemplates(%v) was accepted", bad)
		}
	}
}

// Everything above calls the runtime's own functions. This one goes
// through the SDK exactly as an app does — tako.Site's methods, the
// hooks seam, tako.HTML coming back — because a perfect implementation
// behind an unwired hook is still a feature nobody can call.
func TestTheSDKPathPreparesRendersAndPublishes(t *testing.T) {
	liveSite(t, map[string]string{"layout.html": shell, "home.html": `{{ define "content" }}before{{ end }}`})
	site := tako.Site("web")
	ctx := &requestCtx{Context: context.Background()}

	set, err := site.PrepareTemplates(ctx, templateFS(map[string]string{
		"layout.html": shell,
		"home.html":   `{{ define "content" }}after{{ end }}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Rendered from the candidate, and the live site is still the old one.
	page, err := set.Render(ctx, "home.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(htmlMarkup(page)); !strings.Contains(got, "after") {
		t.Errorf("candidate render = %q", got)
	}
	live, err := site.Render(ctx, "home.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(htmlMarkup(live)); !strings.Contains(got, "before") {
		t.Errorf("the live site changed before UseTemplates: %q", got)
	}

	if err := site.UseTemplates(ctx, set); err != nil {
		t.Fatal(err)
	}
	live, err = site.Render(ctx, "home.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(htmlMarkup(live)); !strings.Contains(got, "after") {
		t.Errorf("after UseTemplates the site still renders %q", got)
	}

	// And the refusal an app branches on reaches it as *tako.TemplateErrors.
	_, err = site.PrepareTemplates(ctx, templateFS(map[string]string{
		"layout.html": shell,
		"broken.html": `{{ define "content" }}{{ .X `,
	}))
	var bad *tako.TemplateErrors
	if !errors.As(err, &bad) {
		t.Fatalf("a parse failure did not arrive as *tako.TemplateErrors: %T %v", err, err)
	}
	if _, ok := bad.Failed["broken.html"]; !ok {
		t.Errorf("Failed is not keyed by template path: %v", bad.Failed)
	}
}

// htmlMarkup reads the markup back out of a tako.HTML through the same
// seam the page handler uses.
func htmlMarkup(h tako.HTML) []byte {
	markup, _, _, _ := hooks.HTMLParts(h)
	return markup
}
