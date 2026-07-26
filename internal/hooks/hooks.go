// Package hooks is the boot seam between generated services and the SDK:
// pkg/runtime wires backends in at startup, and the root tako package
// reads them per operation. Internal on purpose — user code never touches
// backends directly.
package hooks

import (
	"context"

	"github.com/gsandoval/tako/pkg/backends"
)

// Entity is the wired entity backend; nil until boot.
var Entity backends.Entity

// Bucket is the wired bucket backend; nil until boot.
var Bucket backends.Bucket

// Queue is the wired queue backend; nil until boot.
var Queue backends.Queue

// Topic is the wired topic backend; nil until boot.
var Topic backends.Topic

// Signer is the wired URL signer; nil until boot.
var Signer backends.URLSigner

// Cache is the wired cache backend; nil until boot.
var Cache backends.Cache

// Metrics is the wired metrics sink; nil until boot.
var Metrics backends.Metrics

// DeclaredSecrets is set by the SDK at init: it lists every secret name
// declared via tako.Secret, so boot can refuse to start with any unset.
var DeclaredSecrets func() []string

// HandlerInfo is set by the SDK at init: it opens a Handler record for
// the runtime — kind, bound source (queue or topic name), and the
// payload-decoding invoke closure (typed func(tako.Ctx, []byte) error,
// passed as any to avoid an import cycle).
var HandlerInfo func(h any) (kind, source string, invoke any)

// RenderSite renders a site template inside the layout — pkg/runtime
// sets it (RegisterSiteTemplates); site.Render calls through. The ctx
// is the request's context.Context (carries the CSRF token).
var RenderSite func(ctx context.Context, name string, data any) ([]byte, error)

// HTMLParts opens a tako.HTML for the runtime's page handler — the SDK
// sets it at init (typed as any to avoid the import cycle).
var HTMLParts func(h any) (markup []byte, redirect string)
