// Package backends defines the interface contracts that resource
// implementations satisfy. The local and gcp subpackages implement them;
// the conformance subpackage is the shared suite both must pass — the
// contract that keeps them from silently diverging.
package backends
