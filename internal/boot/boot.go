// Package boot wires the local backends into a running set — the one
// place that knows how a local app comes up. runtime.BootLocal (dev,
// generated mains) and tako.NewTestApp (tests) both build on it.
package boot

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/secrets"
	"github.com/gsandoval/tako/pkg/backends/local"
)

// Options configure one local boot.
type Options struct {
	// Dir roots the backends' data (entity.db, buckets/, deadletter/,
	// signing.key).
	Dir string
	// BaseURL is the base for minted signed URLs.
	BaseURL string
	// QueueOptions tune retry policy; zero values mean the stated local
	// defaults (5 × 200ms).
	Queue local.QueueOptions
	// EnvFile, when non-empty, is the .env path whose name=value lines
	// resolve declared secrets. Secrets are process-global (Value takes
	// no Ctx), so this loads into the shared table.
	EnvFile string
}

// App is one booted set of local backends.
type App struct {
	Entity     *local.EntityStore
	Buckets    *local.BucketStore
	Dispatcher *local.QueueDispatcher
	Signer     *local.URLSigner
	Cache      *local.MemoryCache
	Metrics    *local.MetricsLog
}

// Local boots a fresh backend set rooted at opts.Dir and installs it as
// the process-global default (hooks). Test apps additionally carry the
// returned App in their contexts, which takes precedence over the
// globals — that is what isolates parallel test apps.
func Local(opts Options) (*App, error) {
	store, err := local.OpenEntity(opts.Dir)
	if err != nil {
		return nil, err
	}
	buckets := local.NewBucketStore(opts.Dir)
	dispatcher := local.NewQueueDispatcher(opts.Dir, opts.Queue)
	local.WireBucketEvents(buckets, dispatcher)
	signer, err := local.NewURLSigner(opts.Dir, opts.BaseURL)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	if opts.EnvFile != "" {
		resolved, err := local.LoadEnvFile(opts.EnvFile)
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		secrets.Load(resolved)
	}
	app := &App{
		Entity:     store,
		Buckets:    buckets,
		Dispatcher: dispatcher,
		Signer:     signer,
		Cache:      local.NewMemoryCache(),
		Metrics:    local.NewMetricsLog(),
	}
	hooks.Entity = store
	hooks.Bucket = buckets
	hooks.Queue = dispatcher
	hooks.Topic = dispatcher.Topics()
	hooks.Signer = signer
	hooks.Cache = app.Cache
	hooks.Metrics = app.Metrics
	// One lease table per process: dev's whole app IS one instance, and
	// TestApp goroutines contend on it for real.
	hooks.Lease = local.NewMemoryLease()
	hooks.LeaseOwner = leaseOwner()
	return app, nil
}

// leaseOwner names this process in lease records — what an operator
// sees on a wedged lease.
func leaseOwner() string {
	host, _ := os.Hostname()
	var b [3]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b[:]))
}

// Close releases the app's resources.
func (a *App) Close() error { return a.Entity.Close() }
