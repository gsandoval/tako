// Package boot wires the local backends into a running set — the one
// place that knows how a local app comes up. runtime.BootLocal (dev,
// generated mains) and tako.NewTestApp (tests) both build on it.
package boot

import (
	"github.com/gsandoval/tako/internal/hooks"
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
}

// App is one booted set of local backends.
type App struct {
	Entity     *local.EntityStore
	Buckets    *local.BucketStore
	Dispatcher *local.QueueDispatcher
	Signer     *local.URLSigner
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
	app := &App{Entity: store, Buckets: buckets, Dispatcher: dispatcher, Signer: signer}
	hooks.Entity = store
	hooks.Bucket = buckets
	hooks.Queue = dispatcher
	hooks.Topic = dispatcher.Topics()
	hooks.Signer = signer
	return app, nil
}

// Close releases the app's resources.
func (a *App) Close() error { return a.Entity.Close() }
