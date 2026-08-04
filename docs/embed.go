// Package docs carries the user-facing documentation as data, so that
// `tako docs` can serve it from a binary someone installed with
// `go install` and no checkout of this repository.
//
// It is a Go package for one reason: //go:embed patterns cannot escape
// the directory of the package that declares them, so the only place
// that can embed docs/guide and docs/reference is docs itself. Nothing
// else lives here — the server is pkg/docserver, and the markdown is
// the same markdown a reader sees on the repository's file tree.
//
// Deliberately NOT embedded: the working documents at docs/*.md — the
// tech spec, the implementation plan, the agent plan, the task log, the
// reviews. They are how tako gets built, not how it gets used.
//
// The embedded copy is as old as the binary. That is the correct
// behaviour rather than a limitation: the docs a binary serves describe
// the SDK that binary compiles against.
package docs

import "embed"

// FS holds guide/, reference/ and api/ exactly as they sit on disk.
//
// This package is importable because Go has no other way to hand the
// CLI its own documentation; it is tako's internals, not API, like
// everything else outside the root package.
//
//go:embed guide reference api
var FS embed.FS
