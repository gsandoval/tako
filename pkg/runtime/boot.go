package runtime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gsandoval/tako/internal/boot"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/secrets"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
	"github.com/gsandoval/tako/pkg/backends/local"
)

var booted *boot.App

// metricsFlush is how often buffered metrics ship. Cloud Monitoring
// accepts one point per series per 10s, and a minute is the granularity
// its charts read at anyway — so this trades up-to-a-minute of latency
// for a request path that never waits on a write.
const metricsFlush = 60 * time.Second

// Boot wires the service's backends per TAKO_BACKEND (default "local":
// in-process backends under .tako/data). Generated mains call it first.
func Boot() {
	switch backend := os.Getenv("TAKO_BACKEND"); backend {
	case "", "local":
		if err := BootLocal(".tako/data"); err != nil {
			fmt.Fprintf(os.Stderr, "tako: boot: %v\n", err)
			os.Exit(1)
		}
	case "gcp":
		if err := BootGCP(); err != nil {
			fmt.Fprintf(os.Stderr, "tako: boot: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "tako: boot: unknown TAKO_BACKEND %q\n", backend)
		os.Exit(1)
	}
}

// BootGCP wires the cloud backends from the env the deploy injected:
// TAKO_PROJECT (Datastore + GCS + Pub/Sub project), TAKO_APP (bucket
// scoping), TAKO_ENV (Datastore namespace + name prefixes),
// TAKO_SERVICE (dead-topic derivation), TAKO_BUCKET_EVENTS (buckets
// whose writes the runtime announces). Declared secrets resolve from
// Secret Manager here, once — a missing one refuses boot, exactly like
// local's .env. Cache and metrics stay in-process in v0; signed URLs
// are C5 and error with a phase pointer until then.
func BootGCP() error {
	opts := gcpb.Options{
		Project: os.Getenv("TAKO_PROJECT"),
		App:     os.Getenv("TAKO_APP"),
		Env:     os.Getenv("TAKO_ENV"),
		Service: os.Getenv("TAKO_SERVICE"),
	}
	for name, v := range map[string]string{"TAKO_PROJECT": opts.Project, "TAKO_APP": opts.App, "TAKO_ENV": opts.Env} {
		if v == "" {
			return fmt.Errorf("%s is not set — tako deploy injects it, so this is a tako bug", name)
		}
	}
	// Before anything else can log: from here on every line this process
	// writes carries a severity Cloud Logging understands, and requests
	// carry the trace that nests their handler's lines underneath them.
	useCloudLogging(opts.Project)
	c, err := gcpb.New(context.Background(), opts)
	if err != nil {
		return err
	}
	// Package-wide secret resolution (C4 call (a)): every service of
	// the app resolves every declared secret, so SecretHandle.Value
	// never fails at request time in any of them. Freshly deployed
	// grants can lag (IAM propagation), so a first boot retries briefly
	// before failing with the readable text.
	if hooks.DeclaredSecrets != nil {
		for _, name := range hooks.DeclaredSecrets() {
			var value string
			var err error
			deadline := time.Now().Add(90 * time.Second)
			for {
				value, err = c.AccessSecret(context.Background(), name)
				if err == nil || time.Now().After(deadline) {
					break
				}
				fmt.Fprintf(os.Stderr, "tako: boot: secret %q not readable yet (IAM propagation?), retrying: %v\n", name, err)
				time.Sleep(5 * time.Second)
			}
			if err != nil {
				return fmt.Errorf("secret %q is not readable — add it to Secret Manager (preflight prints the exact command) and re-run tako deploy: %v", name, err)
			}
			secrets.Set(name, value)
		}
	}
	if err := checkSecrets(); err != nil {
		return err
	}
	hooks.Entity = gcpb.NewEntityStore(c)
	hooks.Bucket = gcpb.NewBucketStore(c)
	hooks.Queue = gcpb.NewQueueStore(c)
	hooks.Topic = gcpb.NewTopicStore(c)
	hooks.Signer = gcpb.NewSigner(c)
	hooks.Cache = local.NewMemoryCache()
	// Metrics export (U2). The identity comes off the metadata server
	// rather than the env because Cloud Run publishes the region and the
	// instance id there and nowhere else; failing to reach it means this
	// is not Cloud Run, which is a reason to fall back to logging, not to
	// refuse to boot.
	if who, err := gcpb.WhoAmI(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "tako: metrics stay log-only — the instance metadata server is unreachable, so Cloud Monitoring has no region to file them under: %v\n", err)
		hooks.Metrics = local.NewMetricsLog()
	} else {
		metrics := gcpb.NewMetricsStore(c, who)
		metrics.Start(metricsFlush)
		hooks.Metrics = metrics
		// The last flush runs after the drain, so it carries the
		// recordings the final in-flight requests made.
		hooks.OnShutdown(metrics.Close)
	}
	// Leases are objects in the framework bucket — the real §4.4
	// mechanism, shared with the deploy lease.
	hooks.Lease = gcpb.NewLeaseStore(c)
	hooks.LeaseOwner = leaseOwner()
	hooks.CronSkipped = narrateCronSkip
	// Jobs are REAL Cloud Run Job executions: the deploy injects each
	// triggerable job's resource name as TAKO_JOB_<NAME>.
	hooks.RunJob = func(ctx context.Context, name string, params []byte) error {
		resource := os.Getenv("TAKO_JOB_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")))
		if resource == "" {
			return fmt.Errorf("job %q has no execution target — tako deploy injects TAKO_JOB_* from the graph's Run edges, so this is a tako bug (re-run tako deploy, and file it if it persists)", name)
		}
		return c.RunJob(ctx, resource, params)
	}
	// tako.Permanent short-circuits to this service's own dead topic:
	// Pub/Sub cannot express "nack without retry", so PushHandler
	// publishes and acks — one attempt, then dead-letter, like local.
	deadLetter = c.DeadLetter
	// Per-instance cron tickers under autoscaling are not a schedule —
	// crons stay inert in the cloud until the scheduler feature.
	cronInert = true
	return nil
}

// BootLocal wires the in-process backends rooted at dir. Tests use it
// directly with a temp dir until tako.NewTestApp lands (F7); calling it
// again closes the previously booted store first.
func BootLocal(dir string) error {
	if booted != nil {
		_ = booted.Close()
		booted = nil
	}
	// Signed URLs point back at this service's own /_tako/signed/
	// endpoint; the signing key persists under dir so URLs survive
	// reloads.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	// Secrets resolve once, here: .env in the module dir locally, and any
	// declared secret left unset stops the service before it serves.
	app, err := boot.Local(boot.Options{Dir: dir, BaseURL: "http://localhost:" + port, EnvFile: ".env"})
	if err != nil {
		return err
	}
	if err := checkSecrets(); err != nil {
		_ = app.Close()
		return err
	}
	attachQueueConsole(app.Dispatcher, dir)
	watchReplays(app.Dispatcher)
	attachMetricsConsole(app.Metrics)
	// TAKO_CHAOS=<seed> (tako dev --chaos) turns on delivery chaos:
	// duplicates, redelivery after ack, reordering — seeded, so a run
	// reproduces exactly.
	if v := os.Getenv("TAKO_CHAOS"); v != "" {
		seed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("TAKO_CHAOS must be a seed integer, got %q", v)
		}
		app.Dispatcher.EnableChaos(seed)
		attachChaosConsole(app.Dispatcher)
		fmt.Printf("☢ chaos: enabled (seed %d) — deliveries may duplicate, reorder, redeliver\n", seed)
	}
	booted = app
	hooks.CronSkipped = narrateCronSkip
	return nil
}

// narrateCronSkip is the singleflight skip line — a collapsed
// overlapping run is the feature working, never an error.
func narrateCronSkip(name string) {
	fmt.Printf("↻ cron %s skipped — an overlapping run holds the lease\n", name)
}

// leaseOwner names this instance in lease records (the cloud face;
// local boots name themselves in internal/boot).
func leaseOwner() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// checkSecrets refuses to boot with any declared secret unset — that is
// the contract that lets SecretHandle.Value never fail at request time.
func checkSecrets() error {
	if hooks.DeclaredSecrets == nil {
		return nil
	}
	for _, name := range hooks.DeclaredSecrets() {
		if _, ok := secrets.Lookup(name); !ok {
			return fmt.Errorf("secret %q is not set — add it to .env (local) or Secret Manager (cloud)", name)
		}
	}
	return nil
}

// WaitIdle blocks until local queues drain (every published message has
// acked or dead-lettered). Test plumbing until tako.NewTestApp's
// DrainQueues lands in F7.
func WaitIdle(timeout time.Duration) error {
	if booted == nil {
		return nil
	}
	return booted.Dispatcher.WaitIdle(timeout)
}

// SignedURLHandler serves the local signed-URL endpoint
// (/_tako/signed/<token>) against the booted backends. Generated mains
// mount it when the app declares a bucket — on every backend, so on
// gcp (no local boot) it serves the C5 phase pointer instead of
// panicking: signBlob-backed signed URLs are C5's slice, and the
// Signer constructors error the same way.
func SignedURLHandler() http.Handler {
	if booted == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "tako: signed URLs on the gcp backend arrive with C5 — SignedPut/SignedGet error until then", http.StatusNotImplemented)
		})
	}
	return local.SignedURLEndpoint(booted.Signer, booted.Buckets)
}

// SetQueueRate mirrors a WithRateLimit queue's dispatch cap onto the
// local dispatcher (T3). In the cloud the cap lives in the Cloud Tasks
// queue itself, so this is a no-op when nothing local is booted.
func SetQueueRate(queue string, perSec int) {
	if booted != nil {
		booted.Dispatcher.SetRate(queue, perSec)
	}
}

// replayPoll is how often a local run looks for replayed dead letters.
// The CLI and this process are strangers — the CLI renames a file into
// the inbox and has no way to say so — and a restart is not an
// acceptable price for "put that message back", so it polls. One
// ReadDir of a directory that usually does not exist.
const replayPoll = 2 * time.Second

// watchReplays drains the replay inbox for as long as this process
// lives. Dev-only in practice: the cloud path republishes through the
// broker and needs no inbox.
func watchReplays(d *local.QueueDispatcher) {
	go func() {
		for {
			time.Sleep(replayPoll)
			if n, err := d.DrainReplays(); err == nil && n > 0 {
				fmt.Printf("↩ replayed %d dead letter(s) back into delivery\n", n)
			}
		}
	}()
}
