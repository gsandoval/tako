package devserver

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/codegen"
)

// Options configures one tako dev run.
type Options struct {
	// Dir is the app's module directory.
	Dir string
	// Port is the port to serve on; empty means the PORT env var, then
	// 8080.
	Port string
	// Out receives the dev loop's own output (not the app's); nil means
	// os.Stdout.
	Out io.Writer
	// ChaosSeed, when non-zero, runs the app with delivery chaos enabled
	// (tako dev --chaos): duplicates, redelivery after ack, reordering —
	// seeded, so a session reproduces exactly.
	ChaosSeed int64
}

// Run executes the dev loop: analyze → generate → build → run, then watch
// for changes and repeat until ctx is cancelled. Analysis and build
// failures are printed and the last good build keeps serving.
func Run(ctx context.Context, opts Options) error {
	if opts.Port == "" {
		opts.Port = os.Getenv("PORT")
	}
	if opts.Port == "" {
		opts.Port = "8080"
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return err
	}

	s := &session{opts: opts, dir: dir}
	defer s.stopApp()

	fmt.Fprintf(s.out(), "tako dev · %s\n\n", filepath.Base(dir))
	if err := s.cycle(true); err != nil {
		// First cycle failed: nothing is serving yet, but stay up and
		// watch — fixing the file should bring the app up.
		s.printFailure(err)
	}

	watcher, err := watch(dir)
	if err != nil {
		return err
	}
	defer watcher.Close()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// The loop must not retrigger itself: its own writes live
			// under .tako, and each analyze pass READS the source files,
			// which macOS surfaces as attribute-only (Chmod) events.
			if hiddenPath(dir, ev.Name) || ev.Op == fsnotify.Chmod {
				continue
			}
			drain(watcher, 150*time.Millisecond)
			if err := s.cycle(false); err != nil {
				s.printFailure(err)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(s.out(), "watch error: %v\n", err)
		}
	}
}

type session struct {
	opts Options
	dir  string
	app  *exec.Cmd
}

func (s *session) out() io.Writer { return s.opts.Out }

// cycle runs one analyze → generate → build → (re)start pass. On error the
// previous app process is left running.
func (s *session) cycle(first bool) error {
	start := time.Now()

	graph, err := analyzer.Analyze(analyzer.Config{Dir: s.dir})
	if err != nil {
		return err
	}
	svc, err := appService(graph)
	if err != nil {
		return err
	}
	if first {
		fmt.Fprintf(s.out(), "  analyzing   %s\n", CountLine(graph))
	}

	written, err := codegen.Generate(graph, s.dir)
	if err != nil {
		return err
	}
	if first {
		fmt.Fprintf(s.out(), "  generating  %s\n", strings.Join(written, ", "))
	}

	bin := filepath.Join(".tako", "bin", svc.Name)
	build := exec.Command("go", "build", "-o", bin, "./"+filepath.Join(".tako", "gen", svc.Name))
	build.Dir = s.dir
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build failed:\n%s", strings.TrimSpace(string(out)))
	}

	if first {
		fmt.Fprintf(s.out(), "  starting    http://localhost:%s\n\n", s.opts.Port)
		workers, subs := 0, 0
		for _, h := range graph.Handlers {
			switch h.Kind {
			case "worker":
				workers++
			case "subscribe":
				subs++
			}
			fmt.Fprintf(s.out(), "  %s\n", analyzer.HandlerLine(h))
		}
		for _, r := range graph.Resources {
			fmt.Fprintf(s.out(), "  %s\n", analyzer.ResourceLine(r))
		}
		switch {
		case workers > 0 && subs > 0:
			fmt.Fprintln(s.out(), "  local: workers and subscribers run in-process (per-handler services arrive with cloud deploys)")
		case subs > 0:
			fmt.Fprintln(s.out(), "  local: subscribers run in-process (per-handler services arrive with cloud deploys)")
		case workers > 0:
			fmt.Fprintln(s.out(), "  local: workers run in-process (per-worker services arrive with cloud deploys)")
		}
		fmt.Fprintln(s.out())
	}

	s.stopApp()
	if err := s.startApp(filepath.Join(s.dir, bin)); err != nil {
		return err
	}
	if err := waitReady(s.opts.Port, 5*time.Second); err != nil {
		return err
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	if first {
		fmt.Fprintf(s.out(), "ready in %s — watching (backends: local, in-process)\n", elapsed)
	} else {
		fmt.Fprintf(s.out(), "reloaded in %s\n", elapsed)
	}
	return nil
}

func (s *session) startApp(bin string) error {
	cmd := exec.Command(bin)
	cmd.Dir = s.dir
	cmd.Env = append(os.Environ(), "PORT="+s.opts.Port)
	if s.opts.ChaosSeed != 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("TAKO_CHAOS=%d", s.opts.ChaosSeed))
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s.app = cmd
	return nil
}

func (s *session) stopApp() {
	if s.app == nil || s.app.Process == nil {
		return
	}
	_ = s.app.Process.Kill()
	_, _ = s.app.Process.Wait()
	s.app = nil
}

func (s *session) printFailure(err error) {
	suffix := ""
	if s.app != nil {
		suffix = " — still serving last good build"
	}
	fmt.Fprintf(s.out(), "\n✗ analysis failed%s\n%s\n", suffix, indent(err.Error()))
}

func indent(msg string) string {
	lines := strings.Split(strings.TrimRight(msg, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

// hiddenPath reports whether path sits under a hidden directory (or is a
// hidden file) relative to dir — .tako's generated output, data files,
// and .git internals. The watcher can still surface such events (kqueue
// watches whole directories), so the event loop filters them; reloading
// on the loop's own writes would spin forever.
func hiddenPath(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// watch recursively watches dir's directories, skipping hidden ones
// (.tako, .git).
func watch(dir string) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if name := d.Name(); strings.HasPrefix(name, ".") && path != dir {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
	if err != nil {
		watcher.Close()
		return nil, err
	}
	return watcher, nil
}

// drain absorbs the burst of events editors emit for one save, so a save
// triggers one rebuild.
func drain(watcher *fsnotify.Watcher, window time.Duration) {
	timer := time.NewTimer(window)
	defer timer.Stop()
	for {
		select {
		case <-watcher.Events:
			timer.Reset(window)
		case <-timer.C:
			return
		}
	}
}

func waitReady(port string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("service did not start listening on :%s within %s", port, timeout)
}

// appService picks the one service with synchronous handlers — the one
// tako dev builds and runs; local mode collapses workers and subscribers
// into it.
func appService(g *analyzer.AppGraph) (analyzer.Service, error) {
	var apps []analyzer.Service
	for _, svc := range g.Services {
		for _, h := range g.Handlers {
			if h.Service == svc.Name && h.Kind != "worker" && h.Kind != "subscribe" {
				apps = append(apps, svc)
				break
			}
		}
	}
	if len(apps) != 1 {
		return analyzer.Service{}, fmt.Errorf("tako dev serves exactly one app service in the walking skeleton; found %d", len(apps))
	}
	return apps[0], nil
}

// CountLine renders the banner's analyzing summary: endpoints are the
// sync handlers; async handlers are counted by kind. tako test prints
// the same line.
func CountLine(g *analyzer.AppGraph) string {
	endpoints, workers, subs := 0, 0, 0
	for _, h := range g.Handlers {
		switch h.Kind {
		case "worker":
			workers++
		case "subscribe":
			subs++
		default:
			endpoints++
		}
	}
	line := fmt.Sprintf("%d service%s, %d endpoint%s",
		len(g.Services), plural(len(g.Services)), endpoints, plural(endpoints))
	if workers > 0 {
		line += fmt.Sprintf(", %d worker%s", workers, plural(workers))
	}
	if subs > 0 {
		line += fmt.Sprintf(", %d subscriber%s", subs, plural(subs))
	}
	return line
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
