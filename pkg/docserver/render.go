package docserver

import (
	"bytes"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// The markdown pipeline is the one tako already has (spec §2.11,
// settled decision #13): goldmark renders, bluemonday sanitizes. The
// input here is tako's own documentation, which is exactly the argument
// for sanitizing it anyway — a renderer with an exception for trusted
// input is a renderer with an exception, and this one runs on a port.
//
// Two differences from pkg/runtime's, both forced by the content:
//
//   - auto heading IDs, because the docs link to each other's sections
//     (observability.md#shutdown), so the anchors have to exist;
//   - a link renderer, because a destination written for a file tree
//     (entity.md, ../../examples/shop/app.go) is not a URL on a server.
var (
	policyOnce sync.Once
	docPolicy  *bluemonday.Policy
)

var anchorID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9\-_.]*$`)

func policy() *bluemonday.Policy {
	policyOnce.Do(func() {
		p := bluemonday.UGCPolicy()
		// Heading IDs are the anchors; without them every #section link
		// in the docs lands at the top of the page.
		p.AllowAttrs("id").Matching(anchorID).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
		// Code-fence language classes, and the one class the link
		// renderer emits.
		p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).
			OnElements("code", "pre", "span", "a")
		docPolicy = p
	})
	return docPolicy
}

// A LinkKind is what a markdown destination turned out to be.
type LinkKind int

const (
	LinkInternal LinkKind = iota // another embedded page
	LinkAnchor                   // a section of this page
	LinkRepo                     // a file in the tako repository, not embedded
	LinkExternal                 // http(s), mailto
)

// A Link is one destination as it was resolved. Recorded so the link
// check can walk them; nothing serves from this.
type Link struct {
	From   string   // "reference/alerts.md"
	Dest   string   // as written in the markdown
	Kind   LinkKind //
	Target string   // route for Internal, repo-relative path for Repo
	Anchor string   // without the "#"
}

// resolve decides what a markdown destination means, from the page it
// was written on.
func resolve(from, dest string, byRoute map[string]*Page) Link {
	l := Link{From: from, Dest: dest}
	switch {
	case dest == "":
		l.Kind = LinkExternal // nothing to check, nothing to rewrite
		return l
	case strings.HasPrefix(dest, "#"):
		l.Kind, l.Anchor, l.Target = LinkAnchor, dest[1:], ""
		return l
	case strings.Contains(dest, "://"), strings.HasPrefix(dest, "mailto:"):
		l.Kind, l.Target = LinkExternal, dest
		return l
	}
	file, anchor, _ := strings.Cut(dest, "#")
	l.Anchor = anchor
	// Destinations are written for the file tree, so they resolve
	// against the page's directory INSIDE the repository — which is how
	// "../../examples/shop/app.go" comes out as a path a reader can open.
	full := path.Join("docs", path.Dir(from), file)
	if embedded, ok := strings.CutPrefix(full, "docs/"); ok {
		if p, known := byRoute[routeFor(embedded)]; known && strings.HasSuffix(embedded, ".md") {
			l.Kind, l.Target = LinkInternal, p.Route
			return l
		}
	}
	l.Kind, l.Target = LinkRepo, full
	return l
}

// render turns one page's markdown into sanitized HTML, collecting the
// heading anchors it defines and the links it makes.
func render(src string, page *Page, byRoute map[string]*Page) (string, map[string]bool, []Link, error) {
	lr := &linkRenderer{from: page.File, byRoute: byRoute}
	md := goldmark.New(
		goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.Linkify),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
			renderer.WithNodeRenderers(util.Prioritized(lr, 100)),
		),
	)

	source := []byte(src)
	doc := md.Parser().Parse(text.NewReader(source))

	anchors := map[string]bool{}
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok {
			if id, found := h.AttributeString("id"); found {
				if b, ok := id.([]byte); ok {
					anchors[string(b)] = true
				}
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return "", nil, nil, err
	}

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, source, doc); err != nil {
		return "", nil, nil, err
	}
	sanitized := string(policy().SanitizeBytes(buf.Bytes()))
	return combineCodeTabs(sanitized), anchors, lr.links, nil
}

var pairedCodeBlocksRe = regexp.MustCompile(
	`(?s)(?:<p><strong>Go</strong>[ \t]*(.*?)</p>\s*)?` +
		`(<pre><code class="language-go">.*?</code></pre>)\s*` +
		`(?:<p><strong>cfire</strong>[ \t]*(.*?)</p>\s*)?` +
		`(<pre><code class="language-cfire">.*?</code></pre>)`,
)

// combineCodeTabs merges adjacent Go and cfire code blocks into a single
// unified code block with a Go / cfire switch.
func combineCodeTabs(htmlStr string) string {
	return pairedCodeBlocksRe.ReplaceAllStringFunc(htmlStr, func(m string) string {
		sub := pairedCodeBlocksRe.FindStringSubmatch(m)
		if len(sub) < 5 {
			return m
		}
		goMeta := strings.TrimSpace(sub[1])
		goPre := sub[2]
		cfireMeta := strings.TrimSpace(sub[3])
		cfirePre := sub[4]

		var b strings.Builder
		b.WriteString(`<div class="code-tabs" data-active-lang="go">`)
		b.WriteString(`<div class="code-tabs-bar">`)
		b.WriteString(`<div class="code-tabs-switch" role="tablist" aria-label="Code language">`)
		b.WriteString(`<button type="button" class="code-tab-btn on" data-lang="go" role="tab" aria-selected="true">Go</button>`)
		b.WriteString(`<button type="button" class="code-tab-btn" data-lang="cfire" role="tab" aria-selected="false">cfire</button>`)
		b.WriteString(`</div>`)
		if goMeta != "" || cfireMeta != "" {
			b.WriteString(`<div class="code-tabs-meta">`)
			b.WriteString(`<span class="code-tab-caption on" data-lang="go">`)
			b.WriteString(goMeta)
			b.WriteString(`</span>`)
			b.WriteString(`<span class="code-tab-caption" data-lang="cfire">`)
			b.WriteString(cfireMeta)
			b.WriteString(`</span>`)
			b.WriteString(`</div>`)
		}
		b.WriteString(`</div>`)
		b.WriteString(`<div class="code-tab-pane on" data-lang="go">`)
		b.WriteString(goPre)
		b.WriteString(`</div>`)
		b.WriteString(`<div class="code-tab-pane" data-lang="cfire">`)
		b.WriteString(cfirePre)
		b.WriteString(`</div>`)
		b.WriteString(`</div>`)
		return b.String()
	})
}

// linkRenderer replaces goldmark's link rendering. It is registered at a
// lower priority number than the default renderer, which is goldmark's
// way of saying "this one wins".
type linkRenderer struct {
	from    string
	byRoute map[string]*Page
	links   []Link
}

func (r *linkRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindLink, r.renderLink)
}

func (r *linkRenderer) renderLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link)
	link := resolve(r.from, string(n.Destination), r.byRoute)
	if entering {
		r.links = append(r.links, link)
	}
	switch link.Kind {
	case LinkRepo:
		// A file in the repository that is not part of the docs — an
		// example's source, a validation script. There is nothing to
		// serve it from, and a dead <a> would be worse than saying so:
		// the reader gets the sentence plus the path to open.
		//
		// Unless the sentence IS the path. "From [examples/shortlink]
		// (../../examples/shortlink/app.go)" would otherwise read the
		// same thing twice, so a link text the path already contains is
		// replaced by the path rather than followed by it.
		text := nodeText(node, source)
		if text != "" && (strings.HasPrefix(link.Target, text) || strings.HasSuffix(link.Target, text)) {
			if entering {
				w.WriteString(`<code>`)
				w.Write(util.EscapeHTML([]byte(link.Target)))
				w.WriteString(`</code>`)
				return ast.WalkSkipChildren, nil
			}
			return ast.WalkContinue, nil
		}
		if entering {
			w.WriteString(`<span class="repo">`)
		} else {
			w.WriteString(` <code>`)
			w.Write(util.EscapeHTML([]byte(link.Target)))
			w.WriteString(`</code></span>`)
		}
	default:
		if entering {
			w.WriteString(`<a href="`)
			w.Write(util.EscapeHTML([]byte(href(link))))
			if link.Kind == LinkExternal {
				w.WriteString(`" class="ext`)
			}
			w.WriteString(`">`)
		} else {
			w.WriteString(`</a>`)
		}
	}
	return ast.WalkContinue, nil
}

// nodeText is the visible text of a node, which is how a link's own
// words get compared to where it points.
func nodeText(n ast.Node, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(source))
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// href is the destination a browser gets.
func href(l Link) string {
	switch l.Kind {
	case LinkInternal:
		if l.Anchor != "" {
			return l.Target + "#" + l.Anchor
		}
		return l.Target
	case LinkAnchor:
		return "#" + l.Anchor
	default:
		return l.Target
	}
}
