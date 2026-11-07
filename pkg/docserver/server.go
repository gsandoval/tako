package docserver

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gsandoval/tako/docs"
)

// DefaultPort is where `tako docs` serves unless told otherwise.
const DefaultPort = 8090

// Options is the whole surface of `tako docs`.
type Options struct {
	Port      int
	Open      bool
	ExportDir string
}

const footer = "These pages ship inside the tako binary: they describe the SDK this binary compiles against."

// Run renders the docs and serves them until ctx is cancelled.
func Run(ctx context.Context, opts Options) error {
	site, err := Build(docs.FS)
	if err != nil {
		return err
	}

	if opts.ExportDir != "" {
		if err := Export(site, opts.ExportDir); err != nil {
			return err
		}
		fmt.Printf("✓ exported docs to %s (%s)\n", opts.ExportDir, site.Counts())
		return nil
	}

	port := opts.Port
	if port == 0 {
		port = DefaultPort
	}

	// 127.0.0.1, never 0.0.0.0: this is one person reading, and a docs
	// server on the office network is a service nobody decided to run.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		if inUse(err) {
			return fmt.Errorf("port %d is already in use — pass --port to pick another", port)
		}
		return err
	}

	url := fmt.Sprintf("http://localhost:%d", port)
	fmt.Printf("📖 tako docs → %s\n   %s\n   ^C to stop\n", url, site.Counts())
	if opts.Open {
		openBrowser(url)
	}

	srv := &http.Server{Handler: Handler(site), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		close(done)
	}()
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		<-done
		fmt.Println("\ntako docs stopped.")
		return nil
	}
	return err
}

func inUse(err error) bool {
	// syscall.EADDRINUSE is the answer on every platform tako develops
	// on; the string is the belt for the one it does not.
	return errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use")
}

// openBrowser is best-effort by design: a browser that will not open is
// not a reason to fail a server that is already listening, and the URL
// is on screen either way.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// Handler serves a built site. Exported so the tests drive the real
// routes rather than a rehearsal of them.
func Handler(site *Site) http.Handler {
	mux := http.NewServeMux()
	for _, section := range []string{SectionGuide, SectionReference} {
		mux.HandleFunc("GET /"+section, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		})
		mux.HandleFunc("GET /"+section+"/{name...}", func(w http.ResponseWriter, r *http.Request) {
			p := site.Page("/" + section + "/" + r.PathValue("name"))
			if p == nil {
				notFound(w, site)
				return
			}
			render := view{
				Title:   p.Title,
				Section: p.Section,
				Name:    p.Name,
				File:    p.File,
				Route:   p.Route,
				CLIHint: cliTopicHint(p),
				Nav:     nav(site, p.Section, p.Route),
				TOC:     p.TOC,
				Body:    template.HTML(p.Body), //nolint:gosec // sanitized in render()
				Style:   template.CSS(style),
				Footer:  footer,
			}
			write(w, "page", render)
		})
	}
	mux.HandleFunc("GET /assets/{file...}", func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.PathValue("file"))
		if strings.Contains(clean, "..") || clean == "." || clean == "" {
			notFound(w, site)
			return
		}
		data, err := fs.ReadFile(docs.FS, path.Join("assets", clean))
		if err != nil {
			notFound(w, site)
			return
		}
		switch {
		case strings.HasSuffix(clean, ".svg"):
			w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		case strings.HasSuffix(clean, ".png"):
			w.Header().Set("Content-Type", "image/png")
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(docs.FS, "assets/tako_logo_emblem.svg")
		if err != nil {
			notFound(w, site)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /api", func(w http.ResponseWriter, r *http.Request) {
		write(w, "api", view{
			Title:   "API",
			Section: SectionAPI,
			Route:   "/api",
			CLIHint: "tako docs api",
			Nav:     nav(site, SectionAPI, ""),
			Groups:  apiGroups(site),
			Style:   template.CSS(style),
			Footer:  footer,
		})
	})
	mux.HandleFunc("GET /llms.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(site.LLMsTxt()))
	})
	mux.HandleFunc("GET /llms-full.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(site.LLMsFullTxt()))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		write(w, "home", view{Style: template.CSS(style), Footer: footer})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { notFound(w, site) })
	return mux
}

func write(w http.ResponseWriter, name string, v view) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, v); err != nil {
		// The response is already on the wire by the time a template can
		// fail, so there is nothing honest left to send.
		fmt.Printf("tako docs: rendering %s: %v\n", name, err)
	}
}

func notFound(w http.ResponseWriter, site *Site) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_ = tmpl.ExecuteTemplate(w, "notfound", view{Title: "Not found", Style: template.CSS(style), Footer: footer})
}

// nav is the sidebar: the pages of the section being read, or the api
// page's own groups.
func nav(site *Site, section, current string) []navItem {
	var out []navItem
	switch section {
	case SectionGuide:
		var chapters, stories []*Page
		for _, p := range site.Guide {
			switch {
			case p.Name == "":
				out = append(out, navItem{Title: "Overview", Group: true})
				out = append(out, navItem{Title: navTitle(p), Route: p.Route, On: p.Route == current})
			case p.Name[0] >= '0' && p.Name[0] <= '9':
				chapters = append(chapters, p)
			default:
				stories = append(stories, p)
			}
		}
		if len(chapters) > 0 {
			out = append(out, navItem{Title: "Chapters", Group: true})
			for _, p := range chapters {
				out = append(out, navItem{Title: navTitle(p), Route: p.Route, On: p.Route == current})
			}
		}
		if len(stories) > 0 {
			out = append(out, navItem{Title: "Stories", Group: true})
			for _, p := range stories {
				out = append(out, navItem{Title: navTitle(p), Route: p.Route, On: p.Route == current})
			}
		}
	case SectionReference:
		if len(site.RefSections) > 0 {
			for _, rs := range site.RefSections {
				out = append(out, navItem{Title: rs.Name, Group: true})
				for _, p := range rs.Pages {
					out = append(out, navItem{Title: navTitle(p), Route: p.Route, On: p.Route == current})
				}
			}
		} else {
			for _, p := range site.Reference {
				out = append(out, navItem{Title: navTitle(p), Route: p.Route, On: p.Route == current})
			}
		}
	case SectionAPI:
		out = append(out, navItem{Title: "SDK Surface", Group: true})
		for _, g := range apiGroups(site) {
			out = append(out, navItem{Title: g.Section, Route: "/api#" + g.Anchor})
		}
	}
	return out
}

func cliTopicHint(p *Page) string {
	if p == nil {
		return "tako docs"
	}
	if p.Name == "" {
		return "tako docs " + p.Section
	}
	return "tako docs " + p.Name
}

// navTitle keeps the sidebar narrow: a reference page is named by its
// file, the way every other page links to it, and a guide chapter by
// the half of its title before the dash.
func navTitle(p *Page) string {
	if p.Name == "" {
		return "overview"
	}
	if p.Section == SectionReference {
		return p.Name
	}
	if title, _, ok := strings.Cut(p.Title, " — "); ok {
		return title
	}
	return p.Title
}

func apiGroups(site *Site) []apiGroup {
	out := make([]apiGroup, 0, len(site.Groups))
	for _, g := range site.Groups {
		out = append(out, apiGroup{Group: g, Anchor: slug(g.Section)})
	}
	return out
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-':
			b.WriteRune('-')
		}
	}
	return b.String()
}
