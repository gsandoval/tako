package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
)

// SiteOptions carries a declared site's serving behavior — extracted by
// the analyzer, baked into the generated main.
type SiteOptions struct {
	// SPA serves index.html for unknown paths (single-page apps own
	// their routing). Without it, unknown paths 404.
	SPA bool
	// Redirects are permanent (308) path redirects.
	Redirects map[string]string
	// DevProxy, when non-empty AND the backend is local, forwards every
	// site request to a generator's own dev server (Vite/Hugo HMR)
	// instead of serving the FS.
	DevProxy string
}

// fingerprinted matches build-tool cache-busting names: app.3f2a1c.js,
// chunk-BX2A1C9D.js, style-9f86d081ed.css — a hex-ish run of 6+ set off
// by separators before the extension.
var fingerprinted = regexp.MustCompile(`[.\-][0-9a-fA-F]{6,}\.[a-zA-Z0-9]+$`)

// SiteHandler serves a declared site from fsys with the headers nobody
// configures by hand correctly: strong content ETags everywhere (304 on
// If-None-Match), immutable caching for fingerprinted assets, no-cache
// for everything else (HTML above all), and conservative security
// headers on documents. Route precedence belongs to the mux: declared
// API/Page routes are more specific patterns, the site takes the rest.
func SiteHandler(fsys fs.FS, opts SiteOptions) http.Handler {
	if opts.DevProxy != "" && isLocalBackend() {
		target, err := url.Parse(opts.DevProxy)
		if err == nil {
			proxy := httputil.NewSingleHostReverseProxy(target)
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if to, ok := opts.Redirects[r.URL.Path]; ok {
					http.Redirect(w, r, to, http.StatusPermanentRedirect)
					return
				}
				proxy.ServeHTTP(w, r)
			})
		}
	}
	s := &siteServer{fsys: fsys, opts: opts, etags: map[string]string{}}
	return http.HandlerFunc(s.serve)
}

func isLocalBackend() bool {
	backend := os.Getenv("TAKO_BACKEND")
	return backend == "" || backend == "local"
}

type siteServer struct {
	fsys fs.FS
	opts SiteOptions

	mu    sync.Mutex
	etags map[string]string // file path → strong etag (content hash)
}

func (s *siteServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if to, ok := s.opts.Redirects[r.URL.Path]; ok {
		http.Redirect(w, r, to, http.StatusPermanentRedirect)
		return
	}
	// templates/ is site.Render's input, never a static asset.
	if p == "templates" || strings.HasPrefix(p, "templates/") {
		http.NotFound(w, r)
		return
	}
	if p == "." || p == "/" {
		p = "index.html"
	}
	file, err := s.open(p)
	if err != nil {
		// Extensionless paths try the pretty-URL form before falling
		// back (about → about/index.html — generators emit this shape).
		if path.Ext(p) == "" {
			if file, err = s.open(p + "/index.html"); err == nil {
				p = p + "/index.html"
			}
		}
		if err != nil && s.opts.SPA {
			if file, err = s.open("index.html"); err == nil {
				p = "index.html"
			}
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	defer file.Close()

	etag, err := s.etag(p, file)
	if err != nil {
		http.Error(w, "site read error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("ETag", etag)
	if fingerprinted.MatchString(p) {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	ctype := contentTypeOf(p)
	h.Set("Content-Type", ctype)
	if strings.HasPrefix(ctype, "text/html") {
		// Documents get the conservative defaults: report-only CSP to
		// start (spec §2.11), never breaking a site before its owner
		// has read the reports.
		h.Set("Content-Security-Policy-Report-Only", "default-src 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	}
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	// Re-open: the etag pass consumed the reader on first computation.
	fresh, err := s.open(p)
	if err != nil {
		http.Error(w, "site read error", http.StatusInternalServerError)
		return
	}
	defer fresh.Close()
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, fresh)
}

func (s *siteServer) open(p string) (fs.File, error) {
	f, err := s.fsys.Open(p)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err != nil || info.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

// etag computes (and caches) the strong content-hash ETag for a path.
// Embedded sites are immutable per binary, so the cache never staleneses
// in the cloud; local dev restarts the process on changes.
func (s *siteServer) etag(p string, file fs.File) (string, error) {
	s.mu.Lock()
	if tag, ok := s.etags[p]; ok {
		s.mu.Unlock()
		return tag, nil
	}
	s.mu.Unlock()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	tag := `"` + hex.EncodeToString(sum.Sum(nil))[:16] + `"`
	s.mu.Lock()
	s.etags[p] = tag
	s.mu.Unlock()
	return tag, nil
}

// contentTypeOf maps the common web extensions; the stdlib mime table
// covers the rest via http.ServeContent-style sniffing being avoided
// (nosniff is set — types must be right).
func contentTypeOf(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".xml":
		return "application/xml"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".wasm":
		return "application/wasm"
	case ".webmanifest":
		return "application/manifest+json"
	default:
		return "application/octet-stream"
	}
}
