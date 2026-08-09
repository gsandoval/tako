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

	tako "github.com/gsandoval/tako"
)

// SiteOptions carries a declared site's serving behavior — extracted by
// the analyzer, baked into the generated main.
type SiteOptions struct {
	// SPA serves index.html for unknown paths (single-page apps own
	// their routing). Without it, unknown paths 404.
	SPA bool
	// TrustedHTML is the markdown posture (WithTrustedHTML): raw HTML
	// in markdown passes through unsanitized. Never the default.
	TrustedHTML bool
	// Redirects are permanent (308) path redirects.
	Redirects map[string]string
	// DevProxy, when non-empty AND the backend is local, forwards every
	// site request to a generator's own dev server (Vite/Hugo HMR)
	// instead of serving the FS.
	DevProxy string
	// NotFound is the page a 404 renders: a template under templates/,
	// or a static file in the site directory. Empty keeps the plain
	// text.
	NotFound string
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

// notFound answers a static miss. This is the half a handler-side result
// type could never have covered — nothing ran, so there was nothing to
// return a status from — and it is why NotFound is a SiteConfig field
// rather than something a page returns.
//
// The declared page resolves as a TEMPLATE first, then as a static file
// in the site directory, so a generator that already emits 404.html
// just works. Either way a failure falls through to plain text: a 404
// never becomes a 500.
func (s *siteServer) notFound(w http.ResponseWriter, r *http.Request) {
	// The static file is tried FIRST here and only here: it is the
	// generator-emitted 404.html case, and only the site handler can
	// open the site's own FS. Everything after it — the declared
	// template, the app's status page, plain text — is the one writer.
	if s.opts.NotFound != "" && !statusPage.Declared() {
		if body, ok := s.staticNotFound(); ok {
			writeStatusMarkup(w, http.StatusNotFound, body)
			return
		}
	}
	writeStatus(w, r, tako.Status{Code: http.StatusNotFound})
}

// staticNotFound reads the declared page as a file from the site
// directory — the generator-emitted 404.html case.
func (s *siteServer) staticNotFound() ([]byte, bool) {
	file, err := s.open(path.Clean(strings.TrimPrefix(s.opts.NotFound, "/")))
	if err != nil {
		return nil, false
	}
	defer file.Close()
	body, err := io.ReadAll(file)
	if err != nil || len(body) == 0 {
		return nil, false
	}
	return body, true
}

func (s *siteServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeStatus(w, r, tako.Status{Code: http.StatusMethodNotAllowed, Message: "method not allowed"})
		return
	}
	p := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if to, ok := s.opts.Redirects[r.URL.Path]; ok {
		http.Redirect(w, r, to, http.StatusPermanentRedirect)
		return
	}
	// templates/ is site.Render's input, never a static asset.
	if p == "templates" || strings.HasPrefix(p, "templates/") {
		s.notFound(w, r)
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
			s.notFound(w, r)
			return
		}
	}
	defer file.Close()

	etag, err := s.etag(p, file)
	if err != nil {
		writeStatus(w, r, tako.Status{Code: http.StatusInternalServerError})
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
	// nosniff on EVERY file, not only the HTML ones. contentTypeOf maps
	// a fixed table of extensions and this header is what makes that
	// mapping binding — a browser that sniffs past a declared type is
	// exactly what the table exists to prevent, and a stylesheet or a
	// script is as worth pinning as a document. CSP and Referrer-Policy
	// stay HTML-only: they describe a document's privileges, and a
	// stylesheet has none.
	h.Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(ctype, "text/html") {
		securityHeaders(h, false)
	}
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	// Re-open: the etag pass consumed the reader on first computation.
	fresh, err := s.open(p)
	if err != nil {
		writeStatus(w, r, tako.Status{Code: http.StatusInternalServerError})
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

// contentTypeOf maps the common web extensions. Every response this
// file writes carries nosniff, so the browser takes the answer given
// here and does not go looking for a better one — which makes this
// table binding rather than advisory, and a wrong row a served file
// the browser refuses rather than one it guesses about.
//
// That was not true until FL17: nosniff was set only on the HTML
// branch, so this comment described an invariant the code did not
// hold for the .css and .js it mostly serves.
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
