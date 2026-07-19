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
		case _, ok := <-watcher.Events:
			if !ok {
				return nil
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
	if len(graph.Services) != 1 {
		return fmt.Errorf("tako dev serves exactly one service in the walking skeleton; found %d", len(graph.Services))
	}
	svc := graph.Services[0]
	if first {
		fmt.Fprintf(s.out(), "  analyzing   %d service%s, %d endpoint%s\n",
			len(graph.Services), plural(len(graph.Services)),
			len(graph.Handlers), plural(len(graph.Handlers)))
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
		for _, h := range graph.Handlers {
			fmt.Fprintf(s.out(), "  %s → %s (%s)\n", h.Route, analyzer.ShortVar(h), analyzer.ShortPos(h.Pos))
		}
		for _, r := range graph.Resources {
			fmt.Fprintf(s.out(), "  %s\n", analyzer.ResourceLine(r))
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

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
