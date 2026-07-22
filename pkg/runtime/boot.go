package runtime

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gsandoval/tako/internal/boot"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/secrets"
	"github.com/gsandoval/tako/pkg/backends/local"
)

var booted *boot.App

// Boot wires the service's backends per TAKO_BACKEND (default "local":
// in-process backends under .tako/data). Generated mains call it first.
func Boot() {
	switch backend := os.Getenv("TAKO_BACKEND"); backend {
	case "", "local":
		if err := BootLocal(".tako/data"); err != nil {
			fmt.Fprintf(os.Stderr, "tako: boot: %v\n", err)
			os.Exit(1)
		}
	case "gcp":
		fmt.Fprintln(os.Stderr, "tako: boot: the gcp backend arrives in Phase E")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "tako: boot: unknown TAKO_BACKEND %q\n", backend)
		os.Exit(1)
	}
}

// BootLocal wires the in-process backends rooted at dir. Tests use it
// directly with a temp dir until tako.NewTestApp lands (F7); calling it
// again closes the previously booted store first.
func BootLocal(dir string) error {
	if booted != nil {
		_ = booted.Close()
		booted = nil
	}
	// Signed URLs point back at this service's own /_tako/signed/
	// endpoint; the signing key persists under dir so URLs survive
	// reloads.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	// Secrets resolve once, here: .env in the module dir locally, and any
	// declared secret left unset stops the service before it serves.
	app, err := boot.Local(boot.Options{Dir: dir, BaseURL: "http://localhost:" + port, EnvFile: ".env"})
	if err != nil {
		return err
	}
	if err := checkSecrets(); err != nil {
		_ = app.Close()
		return err
	}
	attachQueueConsole(app.Dispatcher, dir)
	attachMetricsConsole(app.Metrics)
	// TAKO_CHAOS=<seed> (tako dev --chaos) turns on delivery chaos:
	// duplicates, redelivery after ack, reordering — seeded, so a run
	// reproduces exactly.
	if v := os.Getenv("TAKO_CHAOS"); v != "" {
		seed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("TAKO_CHAOS must be a seed integer, got %q", v)
		}
		app.Dispatcher.EnableChaos(seed)
		attachChaosConsole(app.Dispatcher)
		fmt.Printf("☢ chaos: enabled (seed %d) — deliveries may duplicate, reorder, redeliver\n", seed)
	}
	booted = app
	return nil
}

// checkSecrets refuses to boot with any declared secret unset — that is
// the contract that lets SecretHandle.Value never fail at request time.
func checkSecrets() error {
	if hooks.DeclaredSecrets == nil {
		return nil
	}
	for _, name := range hooks.DeclaredSecrets() {
		if _, ok := secrets.Lookup(name); !ok {
			return fmt.Errorf("secret %q is not set — add it to .env (local) or Secret Manager (cloud)", name)
		}
	}
	return nil
}

// WaitIdle blocks until local queues drain (every published message has
// acked or dead-lettered). Test plumbing until tako.NewTestApp's
// DrainQueues lands in F7.
func WaitIdle(timeout time.Duration) error {
	if booted == nil {
		return nil
	}
	return booted.Dispatcher.WaitIdle(timeout)
}

// SignedURLHandler serves the local signed-URL endpoint
// (/_tako/signed/<token>) against the booted backends. Generated mains
// mount it when the app declares a bucket.
func SignedURLHandler() http.Handler {
	if booted == nil {
		panic("tako: SignedURLHandler before Boot")
	}
	return local.SignedURLEndpoint(booted.Signer, booted.Buckets)
}
