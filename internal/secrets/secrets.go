// Package secrets is the process-global secret table. Secrets are
// resolved once at startup and read without a Ctx (tako.SecretHandle
// .Value takes none), so — unlike every other backend — they cannot be
// carried per-app in contexts: the table is process-wide by design.
package secrets

import "sync"

var (
	mu     sync.Mutex
	values = map[string]string{}
)

// Load merges resolved secrets into the table (boot, from .env).
func Load(resolved map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	for name, value := range resolved {
		values[name] = value
	}
}

// Set stores one secret (TestApp.SetSecret).
func Set(name, value string) {
	mu.Lock()
	defer mu.Unlock()
	values[name] = value
}

// Unset removes one secret (test cleanup).
func Unset(name string) {
	mu.Lock()
	defer mu.Unlock()
	delete(values, name)
}

// Lookup reads one secret.
func Lookup(name string) (string, bool) {
	mu.Lock()
	defer mu.Unlock()
	v, ok := values[name]
	return v, ok
}
