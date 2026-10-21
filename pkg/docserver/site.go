// Package docserver serves tako's documentation — the guide, the
// feature reference, and the exported SDK surface — from the copy
// embedded in the binary.
//
// Everything is rendered once, at startup, into memory. That costs a
// few milliseconds and buys two things: a page that fails to render
// fails when the server starts rather than when someone clicks it, and
// serving is a map lookup with no markdown in the request path.
package docserver

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Section names, which are also the first path segment of every route.
const (
	SectionGuide     = "guide"
	SectionReference = "reference"
	SectionAPI       = "api"
)

// A Page is one rendered markdown file.
type Page struct {
	Section  string // "guide" or "reference"
	Name     string // "01-hello", or "" for the section index
	Route    string // "/guide/01-hello"
	File     string // "guide/01-hello.md", relative to docs/
	Title    string // the H1, with the leading "# " removed
	Markdown string // raw markdown source
	Body     string // sanitized HTML
	Anchors  map[string]bool
}

// A Site is the whole documentation set, rendered.
type Site struct {
	Guide     []*Page // in reading order: index, then chapters, then the stories
	Reference []*Page // in the order reference/README.md groups them
	byRoute   map[string]*Page

	Surface string   // raw api/surface.txt
	Symbols []Symbol // every exported symbol, in lock order
	Groups  []Group  // the api page, grouped the way the reference groups itself

	// Links is every link found in the markdown, recorded as it was
	// rewritten. Nothing at runtime reads it; the link check does.
	Links []Link
}

// Page returns the page at a route, or nil.
func (s *Site) Page(route string) *Page { return s.byRoute[route] }

// Counts is the line under the URL on startup.
func (s *Site) Counts() string {
	symbols := 0
	for _, sym := range s.Symbols {
		symbols += 1 + len(sym.Methods)
	}
	return fmt.Sprintf("guide (%d chapters) · reference (%d pages) · api (%d symbols)",
		len(s.Guide), len(s.Reference), symbols)
}

// Build renders every embedded page. fsys is the docs package's FS: it
// has guide/, reference/ and api/ at its root.
func Build(fsys fs.FS) (*Site, error) {
	site := &Site{byRoute: map[string]*Page{}}

	sources := map[string]string{} // file -> markdown
	for _, section := range []string{SectionGuide, SectionReference} {
		entries, err := fs.ReadDir(fsys, section)
		if err != nil {
			return nil, fmt.Errorf("reading %s/: %w", section, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			file := path.Join(section, e.Name())
			raw, err := fs.ReadFile(fsys, file)
			if err != nil {
				return nil, err
			}
			sources[file] = string(raw)
			p := &Page{
				Section:  section,
				Name:     strings.TrimSuffix(e.Name(), ".md"),
				File:     file,
				Title:    firstHeading(string(raw)),
				Markdown: string(raw),
			}
			if p.Name == "README" {
				p.Name = ""
			}
			p.Route = "/" + section + "/" + p.Name
			site.byRoute[p.Route] = p
		}
	}
	if len(site.byRoute) == 0 {
		return nil, fmt.Errorf("no documentation was embedded")
	}

	// Rendering is a second pass because a link may point at any page,
	// including one that has not been read yet.
	for file, src := range sources {
		p := site.byRoute[routeFor(file)]
		body, anchors, links, err := render(src, p, site.byRoute)
		if err != nil {
			return nil, fmt.Errorf("rendering %s: %w", file, err)
		}
		p.Body, p.Anchors = body, anchors
		site.Links = append(site.Links, links...)
	}
	sort.Slice(site.Links, func(i, j int) bool {
		a, b := site.Links[i], site.Links[j]
		if a.From != b.From {
			return a.From < b.From
		}
		return a.Dest < b.Dest
	})

	surface, err := fs.ReadFile(fsys, path.Join(SectionAPI, "surface.txt"))
	if err != nil {
		return nil, fmt.Errorf("reading the SDK surface: %w", err)
	}
	site.Surface = string(surface)
	site.Symbols, err = parseSurface(string(surface))
	if err != nil {
		return nil, err
	}

	refIndex, ok := sources[path.Join(SectionReference, "README.md")]
	if !ok {
		return nil, fmt.Errorf("the reference has no README.md, which is where its grouping is written")
	}
	site.Reference = referenceOrder(refIndex, site.byRoute)
	site.Guide = guideOrder(site.byRoute)
	site.Groups = group(site.Symbols, refIndex, site.byRoute)
	return site, nil
}

// routeFor maps "reference/entity.md" to "/reference/entity", and any
// README.md to its section index.
func routeFor(file string) string {
	section, name, _ := strings.Cut(file, "/")
	name = strings.TrimSuffix(name, ".md")
	if name == "README" {
		name = ""
	}
	return "/" + section + "/" + name
}

// guideOrder is the index, then the numbered chapters in their number's
// order, then whatever else the guide holds, alphabetically. The guide's
// own README lists the chapters in prose rather than in a list tako
// could parse, and a numeric filename prefix already carries the order.
func guideOrder(byRoute map[string]*Page) []*Page {
	var numbered, rest []*Page
	var index *Page
	for _, p := range byRoute {
		if p.Section != SectionGuide {
			continue
		}
		switch {
		case p.Name == "":
			index = p
		case p.Name[0] >= '0' && p.Name[0] <= '9':
			numbered = append(numbered, p)
		default:
			rest = append(rest, p)
		}
	}
	byName := func(pages []*Page) {
		sort.Slice(pages, func(i, j int) bool { return pages[i].Name < pages[j].Name })
	}
	byName(numbered)
	byName(rest)
	out := make([]*Page, 0, len(byRoute))
	if index != nil {
		out = append(out, index)
	}
	return append(append(out, numbered...), rest...)
}

// firstHeading is the page's H1. Titles are read from the markdown
// rather than kept in a table here, so a retitled page retitles itself.
func firstHeading(src string) string {
	for _, line := range strings.Split(src, "\n") {
		if after, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(strings.ReplaceAll(after, "`", ""))
		}
	}
	return ""
}
