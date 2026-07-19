// Package conformance is the shared test suite every backend must pass —
// one suite per resource contract, run against local always and gcp from
// Phase E on (go test ./pkg/backends/conformance -backend=local). Any
// behavior difference between backends becomes a case here first, then a
// fix.
package conformance
