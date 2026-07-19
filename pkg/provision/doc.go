// Package provision is the stateless GCP reconciler: it computes desired
// state from the AppGraph, observes live state by label, diffs via
// spec-hash annotations, and applies additive-first. No Terraform, no
// state file; data destruction is never automatic.
package provision
