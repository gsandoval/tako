package tako

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsandoval/tako/internal/trace"
	"github.com/gsandoval/tako/pkg/backends"
)

// DocsLevel selects a preset security slice of project documentation to
// generate and publish for a Docs instance.
type DocsLevel string

const (
	// DocsPublic is the default level: it publishes only the external API
	// surface (APIs) and omits internal-only routes (Route{Private: true}),
	// architecture topology, schedules, resources, roles, footprints, and
	// IAM permissions.
	DocsPublic DocsLevel = "public"

	// DocsInternal publishes operational topology without security-sensitive
	// access control or capacity sizing: APIs, Schedules, Architecture, and
	// Resources. Roles, Footprints, and Permissions are omitted.
	DocsInternal DocsLevel = "internal"

	// DocsFull publishes every documentation slice: APIs, Schedules,
	// Architecture, Resources, Roles, Footprints, and Permissions.
	DocsFull DocsLevel = "full"
)

// DocsConfig configures a project documentation bundle declared with
// tako.Docs.
//
// Documentation is disabled by default: an app that declares no tako.Docs
// instance generates and provisions no documentation buckets or routes.
//
// Slice selection works in two ways:
//   - Security preset: set Level to DocsPublic (default), DocsInternal, or
//     DocsFull.
//   - Component cherry-picking: set any combination of APIs, Schedules,
//     Architecture, Resources, Roles, Footprints, and Permissions to true.
//     When Level is omitted and one or more component booleans are true,
//     only the explicitly enabled components are included. When Level is
//     also set, true component booleans are unioned with the preset.
type DocsConfig struct {
	// Public controls the GCS bucket's IAM posture on GCP. False (the
	// default) keeps the bucket private so only services with a static read
	// edge to the DocsHandle receive roles/storage.objectViewer. True grants
	// roles/storage.objectViewer to allUsers on the dedicated GCS bucket and
	// enables GCS static website hosting (index.html / 404.html).
	Public bool

	// Level selects a preset bundle of documentation slices (DocsPublic,
	// DocsInternal, or DocsFull).
	Level DocsLevel

	// Individual documentation slices.
	APIs         bool
	Schedules    bool
	Architecture bool
	Resources    bool
	Roles        bool
	Footprints   bool
	Permissions  bool
}

// DocsReq is the request type bound by DocsHandle.Serve when mounted on a
// wildcard tako.Page("GET /docs/{path...}", docs.Serve) route.
type DocsReq struct {
	Path string `path:"path"`
}

// Docs declares a generated project documentation bundle and its dedicated
// GCS bucket (<project>-<app>-docs-<env>-<name>).
//
// An app can declare multiple Docs instances with different slice presets
// and access controls — for example, a public API reference and an
// admin-gated operational architecture manual.
func Docs(name string, cfg ...DocsConfig) *DocsHandle {
	return &DocsHandle{name: name, cfg: cfg}
}

// DocsHandle is a handle to a declared project documentation bundle.
type DocsHandle struct {
	name string
	cfg  []DocsConfig
}

// Serve is a tako.Page handler method that serves the multi-page HTML
// documentation bundle from the instance's dedicated bucket:
//
//	var PublicDocs = tako.Page("GET /docs/{path...}", publicDocs.Serve)
//	var OpsDocs    = tako.Page("GET /ops/docs/{path...}", opsDocs.Serve, tako.Route{Allow: Admins})
func (d *DocsHandle) Serve(ctx Ctx, req DocsReq) (HTML, error) {
	return d.render(ctx, "docs.Serve", req.Path)
}

// Render fetches and returns a single documentation page from the instance's
// dedicated bucket (for example, "index.html", "apis", or "architecture").
func (d *DocsHandle) Render(ctx Ctx, pagePath string) (HTML, error) {
	return d.render(ctx, "docs.Render", pagePath)
}

// LLMsTxt is a tako.API handler method that serves the generated llms.txt
// reference for this documentation bundle as text/plain; charset=utf-8:
//
//	var LLMsTxt = tako.API("GET /llms.txt", publicDocs.LLMsTxt)
func (d *DocsHandle) LLMsTxt(ctx Ctx, _ struct{}) (Blob, error) {
	sctx, finish := trace.StartSpan(ctx, "docs", "docs.LLMsTxt", "docs", d.name, "key", "llms.txt")
	defer finish(nil)
	bucketName := "docs:" + d.name
	blob, err := bucketBackend(ctx).Get(sctx, bucketName, "llms.txt")
	if errors.Is(err, backends.ErrNotFound) && Environment() == EnvLocal {
		if fallback, ferr := readLocalDocsFallback(d.name, "llms.txt"); ferr == nil {
			return Blob{
				Reader:      bytes.NewReader(fallback),
				ContentType: "text/plain; charset=utf-8",
				Size:        int64(len(fallback)),
			}, nil
		}
	}
	if errors.Is(err, backends.ErrNotFound) {
		return Blob{}, NotFound()
	}
	if err != nil {
		return Blob{}, err
	}
	return Blob{
		Reader:      blob.Reader,
		ContentType: "text/plain; charset=utf-8",
		Size:        blob.Size,
		ETag:        blob.ETag,
	}, nil
}

func (d *DocsHandle) render(ctx Ctx, op, pagePath string) (HTML, error) {
	sctx, finish := trace.StartSpan(ctx, "docs", op, "docs", d.name, "key", pagePath)
	defer finish(nil)
	key, ct, ok := normalizeDocsKey(pagePath)
	if !ok {
		return HTML{}, NotFound()
	}
	bucketName := "docs:" + d.name
	blob, err := bucketBackend(ctx).Get(sctx, bucketName, key)
	if errors.Is(err, backends.ErrNotFound) && Environment() == EnvLocal {
		if fallback, ferr := readLocalDocsFallback(d.name, key); ferr == nil {
			return HTML{markup: fallback, contentType: ct}, nil
		}
	}
	if errors.Is(err, backends.ErrNotFound) {
		return HTML{}, NotFound()
	}
	if err != nil {
		return HTML{}, err
	}
	raw, err := io.ReadAll(blob.Reader)
	if err != nil {
		return HTML{}, err
	}
	return HTML{markup: raw, contentType: ct}, nil
}

func normalizeDocsKey(raw string) (key, contentType string, ok bool) {
	p := strings.TrimSpace(raw)
	p = strings.Trim(p, "/")
	if strings.Contains(p, "..") || strings.Contains(p, `\`) || strings.ContainsRune(p, 0) {
		return "", "", false
	}
	if p == "" {
		return "index.html", "text/html; charset=utf-8", true
	}
	if strings.Contains(p, "/") {
		parts := strings.Split(p, "/")
		if len(parts) == 2 && parts[1] == "index.html" {
			p = parts[0] + ".html"
		} else if len(parts) == 2 {
			p = parts[1]
		}
	}
	switch {
	case strings.HasSuffix(p, ".html"):
		return p, "text/html; charset=utf-8", true
	case strings.HasSuffix(p, ".json"):
		return p, "application/json; charset=utf-8", true
	case strings.HasSuffix(p, ".txt"):
		return p, "text/plain; charset=utf-8", true
	case !strings.Contains(filepath.Base(p), "."):
		return p + ".html", "text/html; charset=utf-8", true
	default:
		return "", "", false
	}
}

func readLocalDocsFallback(name, key string) ([]byte, error) {
	rel := filepath.Join(".tako", "gen", "docs", name, filepath.FromSlash(key))
	dir, err := os.Getwd()
	if err != nil {
		return os.ReadFile(rel)
	}
	for i := 0; i < 4; i++ {
		cand := filepath.Join(dir, rel)
		if data, rerr := os.ReadFile(cand); rerr == nil {
			return data, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, os.ErrNotExist
}
