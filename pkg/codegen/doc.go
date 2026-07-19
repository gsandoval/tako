// Package codegen turns an AppGraph into generated per-service main
// packages under .tako/gen/<service>/ — human-readable route registration,
// static request binding, async envelope handling, and resource wiring.
// Generated output is never hand-edited; fixes go in the generator.
package codegen
