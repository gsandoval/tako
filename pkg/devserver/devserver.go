package devserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/gsandoval/tako/internal/routes"
	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/build"
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
		// A start failure is not an edit away from working: no file in
		// this directory frees a port. Watching would leave a dev loop
		// running that can never come up, so it exits and says why.
		var foreign *foreignListenerError
		if errors.As(err, &foreign) || errors.Is(err, errExited) {
			return err
		}
		// Anything else: nothing is serving yet, but stay up and watch —
		// fixing the file should bring the app up.
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
			if hiddenPath(dir, ev.Name) || s.ownWrite(ev.Name) || ev.Op == fsnotify.Chmod {
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
	// buildOuts are the site build-output dirs (absolute) from the last
	// analyze — the loop's OWN writes, like .tako, never reload
	// triggers (a WithBuild that rewrites its output every cycle would
	// otherwise spin dev forever).
	buildOuts []string
	// guard is the write end of the running child's stdin leash.
	guard *os.File
	// nonce identifies the running child to waitReady. Fresh per start.
	nonce string
	// exited closes when the running child has been reaped.
	exited chan struct{}
}

// ownWrite reports whether path sits under a build-output dir.
func (s *session) ownWrite(path string) bool {
	for _, out := range s.buildOuts {
		if path == out || strings.HasPrefix(path, out+string(filepath.Separator)) {
			return true
		}
	}
	return false
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
	s.buildOuts = s.buildOuts[:0]
	for _, r := range graph.Resources {
		if r.Kind == "site" && r.BuildOut != "" {
			s.buildOuts = append(s.buildOuts, filepath.Join(s.dir, filepath.FromSlash(r.BuildOut)))
		}
	}
	if first {
		fmt.Fprintf(s.out(), "  analyzing   %s\n", CountLine(graph))
	}

	// Frontend builds re-run on changes (§2.11) — before Generate, which
	// stages the output for embedding. Proxied sites (WithDevProxy)
	// declare no build or skip serving it locally anyway.
	if err := build.Sites(s.dir, graph, func(format string, args ...any) {
		fmt.Fprintf(s.out(), "  "+format+"\n", args...)
	}); err != nil {
		return err
	}

	// After the build, because an unbuilt site has no files to shadow —
	// and on EVERY cycle, not just the first: the warning's moment is
	// the edit that adds the route, which is mid-session.
	for _, sh := range analyzer.ShadowedSiteFiles(s.dir, graph) {
		fmt.Fprintf(s.out(), "  %s\n", sh.Warning())
	}

	written, err := codegen.Generate(graph, s.dir)
	if err != nil {
		return err
	}
	if first {
		// Mains only: the test harness is generated here too, and a dev
		// loop is not the place to mention it.
		fmt.Fprintf(s.out(), "  generating  %s\n", strings.Join(codegen.Mains(written), ", "))
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
	if err := waitReady(s.opts.Port, s.nonce, s.exited, 5*time.Second); err != nil {
		// No `ready` line above this: the last thing the terminal says
		// has to be the truth, and a success line followed by a failure
		// reads as noise after a success.
		s.stopApp()
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
	// A fresh nonce per start: it is how waitReady tells this child from
	// anything else already answering on the port, so reusing one across
	// restarts would let a previous child vouch for the current one.
	nonce, err := newNonce()
	if err != nil {
		return err
	}
	s.nonce = nonce
	cmd.Env = append(os.Environ(), "PORT="+s.opts.Port, "TAKO_DEV_GUARD=1", "TAKO_DEV_NONCE="+nonce)
	if s.opts.ChaosSeed != 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("TAKO_CHAOS=%d", s.opts.ChaosSeed))
	}
	// The service's stdin is a pipe whose write end only THIS process
	// holds: when tako dev dies — Ctrl-C, SIGKILL, closed terminal —
	// the OS closes it, the child's stdin EOFs, and runtime.Serve
	// exits. No more orphaned service binaries holding ports and
	// entity-store locks across sessions.
	guardR, guardW, err := os.Pipe()
	if err != nil {
		return err
	}
	s.exited = make(chan struct{})
	cmd.Stdin = guardR
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		guardR.Close()
		guardW.Close()
		return err
	}
	guardR.Close() // the child holds its copy
	if s.guard != nil {
		s.guard.Close() // release the PREVIOUS child's leash
	}
	s.guard = guardW
	s.app = cmd
	// ONE waiter for the child, here, so that "it exited" is a fact
	// anyone can observe by receiving on the channel. waitReady watches
	// it to stop waiting the moment a start fails — before this, a child
	// that died at once still cost the full readiness timeout — and
	// stopApp waits on it instead of reaping the process itself, because
	// two callers of Wait on one process is a race.
	exited := s.exited
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	return nil
}

func (s *session) stopApp() {
	if s.app == nil || s.app.Process == nil {
		return
	}
	_ = s.app.Process.Kill()
	if s.exited != nil {
		<-s.exited // the Wait goroutine started in startApp reaps it
		s.exited = nil
	}
	if s.guard != nil {
		s.guard.Close()
		s.guard = nil
	}
	s.app = nil
}

func (s *session) printFailure(err error) {
	// A start failure is not an analysis failure, and calling it one
	// sends the reader to the wrong file.
	var foreign *foreignListenerError
	switch {
	case errors.As(err, &foreign):
		fmt.Fprintf(s.out(), "\n✗ %s\n", err)
		return
	case errors.Is(err, errExited):
		fmt.Fprintf(s.out(), "\n✗ the service exited during startup — its own error is above\n")
		return
	}
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

// waitReady blocks until the child answers with ITS OWN nonce, the
// child exits, or the timeout runs out.
//
// The identity check is the whole of it. A TCP dial only reports that
// the port is answered, not by whom, so when another process already
// held the port the old probe succeeded immediately and `tako dev`
// printed `ready` — about a stranger's server, while its own child was
// dying with an address-in-use error a line later. The browser then got
// the other app's pages, which reads as a broken version of the app you
// meant to run rather than as a port collision. So the question this
// asks is not "is anything listening" but "is what is listening mine".
func waitReady(port, nonce string, exited <-chan struct{}, timeout time.Duration) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	url := "http://127.0.0.1:" + port + routes.DevReady
	deadline := time.Now().Add(timeout)
	foreign := false
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			// The child is gone; it printed its own reason on the way
			// out (runtime.Serve names an address-in-use collision and
			// what to do about it). Waiting the rest of the timeout for
			// a process that no longer exists tells nobody anything.
			return errExited
		default:
		}
		switch status, got, err := probe(client, url); {
		case err != nil:
			// Nothing listening yet, or not listening well enough to
			// answer. Keep waiting.
		case status == http.StatusOK && got == nonce:
			return nil
		case status == http.StatusOK:
			// A tako service answered with somebody else's nonce: a
			// second dev loop on this port. That is proof, so say it now.
			return &foreignListenerError{port: port}
		default:
			// SOMETHING answered, but not with an identity — a 404, a
			// redirect, a 500. That is not proof of a stranger, and
			// treating it as proof is what made `tako dev` kill its own
			// child: a service with no reserved mux answered 404 here
			// and was declared foreign while it was serving the app
			// perfectly (item 35).
			//
			// The child's own exit is the reliable signal for a real
			// collision — it fails to bind and says so — and that is
			// checked at the top of this loop. So this only colours the
			// message if we run out of time.
			foreign = true
		}
		time.Sleep(10 * time.Millisecond)
	}
	if foreign {
		return &unidentifiedListenerError{port: port, timeout: timeout}
	}
	return fmt.Errorf("service did not start listening on :%s within %s", port, timeout)
}

// errExited says the child died while we waited. It carries no text of
// its own: the child has already explained itself on stderr.
var errExited = errors.New("the service exited during startup")

// foreignListenerError is a port collision seen from the dev loop's
// side, named as the thing it actually is.
type foreignListenerError struct{ port string }

func (e *foreignListenerError) Error() string {
	return fmt.Sprintf("port %s is answering as ANOTHER tako dev — two dev loops, one port.\n"+
		"Set PORT (e.g. PORT=8090 tako dev) or stop the other one", e.port)
}

// unidentifiedListenerError is the weaker claim, and the honest one:
// something answered and never identified itself, and the child never
// said it was ready either.
//
// It is deliberately not the same sentence as a collision. The old one
// told people to free a port that tako itself had just released, so
// `lsof` came back empty and the advice could not be followed.
type unidentifiedListenerError struct {
	port    string
	timeout time.Duration
}

func (e *unidentifiedListenerError) Error() string {
	return fmt.Sprintf("port %s answered within %s, but never identified itself as this app.\n"+
		"Something else may hold it (`lsof -i :%s`), or this service is built from stale\n"+
		"generated code — `tako dev` regenerates, so a plain `go run` of an old binary can do this",
		e.port, e.timeout, e.port)
}

// probe asks the running child to identify itself. A non-200, or a body
// that is not a nonce, is "not mine" rather than an error.
// probe returns the status and body of one identity check. The STATUS
// is returned rather than folded away, because "404, no such route" and
// "200, the wrong nonce" are different facts and only the second one is
// evidence of another dev loop.
func probe(client *http.Client, url string) (int, string, error) {
	res, err := client.Get(url)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 128))
	if err != nil {
		return res.StatusCode, "", err
	}
	return res.StatusCode, string(body), nil
}

// newNonce mints the per-start identity. It only has to be
// unguessable-enough to not collide with a concurrent dev loop's.
func newNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// appService picks the one PUBLIC app service — the one tako dev
// builds and runs on PORT. Local mode collapses everything else into
// that process: workers, subscribers, crons, jobs, and singletons
// register in-process, and PRIVATE route services (§2.12 callees like
// billing) need no server at all — Endpoint.Call is a direct function
// call locally, breakpoints and all.
func appService(g *analyzer.AppGraph) (analyzer.Service, error) {
	var apps []analyzer.Service
	for _, svc := range g.Services {
		if svc.Job || svc.Singleton {
			continue // in-process via RegisterJob/RegisterSingleton
		}
		public := false
		for _, h := range g.Handlers {
			if h.Service != svc.Name {
				continue
			}
			if (h.Kind == "api" || h.Kind == "page" || h.Kind == "cron") && !h.Private {
				public = true
				break
			}
		}
		if public {
			apps = append(apps, svc)
			continue
		}
		// A site alone is an app too (W1: service-from-site).
		for _, r := range g.Resources {
			if r.Kind == "site" && r.Package == svc.Package && svc.Name == filepathBase(svc.Package) {
				apps = append(apps, svc)
				break
			}
		}
	}
	if len(apps) == 0 {
		return analyzer.Service{}, fmt.Errorf("tako dev found no public app service — every route is private or async; add a public API, Page, or Site")
	}
	if len(apps) > 1 {
		names := make([]string, len(apps))
		for i, a := range apps {
			names[i] = a.Name
		}
		return analyzer.Service{}, fmt.Errorf("tako dev serves one public app service on one port; found %d (%s) — multi-service dev routing is a later slice", len(apps), strings.Join(names, ", "))
	}
	return apps[0], nil
}

func filepathBase(importPath string) string {
	if i := strings.LastIndexByte(importPath, '/'); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

// CountLine renders the banner's analyzing summary: endpoints are the
// sync handlers; async handlers are counted by kind. tako test prints
// the same line.
func CountLine(g *analyzer.AppGraph) string {
	endpoints, workers, subs, crons := 0, 0, 0, 0
	for _, h := range g.Handlers {
		switch h.Kind {
		case "worker":
			workers++
		case "subscribe":
			subs++
		case "cron":
			crons++
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
	if crons > 0 {
		line += fmt.Sprintf(", %d cron%s", crons, plural(crons))
	}
	sites := 0
	for _, r := range g.Resources {
		if r.Kind == "site" {
			sites++
		}
	}
	if sites > 0 {
		line += fmt.Sprintf(", %d site%s", sites, plural(sites))
	}
	return line
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
