// Package tako is the user-facing SDK for the Tako framework.
//
// Tako applications are plain Go packages: infrastructure and behavior are
// both declared as package-level vars via constructors — resources
// (tako.Bucket("photos")) and handlers (tako.Worker(thumbs, makeThumb)).
// Constructors are side-effect-free records: they register intent and never
// touch I/O, so package initialization order can never matter observably.
//
// The surface follows six rules (see docs/tako-tech-spec.md Part 1):
// everything is a declared noun; one constructor shape
// tako.Kind("name", ...Option); one method shape (Ctx first, (T, error) or
// error out); one verb vocabulary (keyed stores share Get/Put/Delete/Exists,
// message carriers share Publish); one async handler shape func(Ctx, T) error;
// and no primitive owns a general concept.
//
// This package is the single source of truth for the exported API; the
// exported symbol set is snapshotted by sdk_lock_test.go.
package tako
