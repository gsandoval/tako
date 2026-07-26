package runtime

import (
	"bytes"
	"sync"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// The markdown pipeline (spec §2.11, settled decision #13): goldmark
// renders, bluemonday sanitizes — user-supplied markdown is an XSS
// vector, so scripts, handlers, styles, and iframes VANISH from the
// output by default. WithTrustedHTML is the explicit site-level
// opt-out: goldmark passes raw HTML through and nothing sanitizes —
// for admin-authored content the app owner vouches for.

var (
	markdownOnce      sync.Once
	sanitizedRenderer goldmark.Markdown
	trustedRenderer   goldmark.Markdown
	ugcPolicy         *bluemonday.Policy
)

func markdownInit() {
	markdownOnce.Do(func() {
		// CommonMark + the GFM extras people actually write (tables,
		// strikethrough, autolinks). Raw HTML is emitted by goldmark and
		// then stripped or kept by policy — one renderer shape, the
		// sanitizer is the guarantee.
		sanitizedRenderer = goldmark.New(
			goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.Linkify),
			goldmark.WithRendererOptions(html.WithUnsafe()),
		)
		trustedRenderer = sanitizedRenderer
		// UGCPolicy: bluemonday's user-generated-content profile —
		// exactly the elements markdown produces (headings, emphasis,
		// links, code, lists, tables, images), rel=nofollow forced on
		// links, everything else stripped. Code fence language classes
		// survive for future highlighting.
		p := bluemonday.UGCPolicy()
		p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code")
		ugcPolicy = p
	})
}

// RenderMarkdown renders markdown source to HTML under the site's
// declared posture: sanitized by default, raw when trusted.
func RenderMarkdown(src string, trusted bool) ([]byte, error) {
	markdownInit()
	var buf bytes.Buffer
	r := sanitizedRenderer
	if trusted {
		r = trustedRenderer
	}
	if err := r.Convert([]byte(src), &buf); err != nil {
		return nil, err
	}
	if trusted {
		return buf.Bytes(), nil
	}
	return ugcPolicy.SanitizeBytes(buf.Bytes()), nil
}
