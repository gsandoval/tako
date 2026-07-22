package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// RunCache runs the cache-contract suite against fresh caches from open:
// the keyed-store verbs, ErrNotFound on absent AND expired entries, and
// TTL expiry (real, short TTLs — expiry is wall-clock behavior).
func RunCache(t *testing.T, open func(t *testing.T) backends.Cache) {
	ctx := context.Background()

	t.Run("absent key is ErrNotFound", func(t *testing.T) {
		c := open(t)
		if _, err := c.Get(ctx, "feed", "nope"); !errors.Is(err, backends.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		ok, err := c.Exists(ctx, "feed", "nope")
		if err != nil || ok {
			t.Fatalf("exists = %v, %v", ok, err)
		}
	})

	t.Run("put then get roundtrips", func(t *testing.T) {
		c := open(t)
		if err := c.Put(ctx, "feed", "k", []byte(`{"a":1}`), 0); err != nil {
			t.Fatal(err)
		}
		got, err := c.Get(ctx, "feed", "k")
		if err != nil || string(got) != `{"a":1}` {
			t.Fatalf("got = %s, %v", got, err)
		}
		ok, err := c.Exists(ctx, "feed", "k")
		if err != nil || !ok {
			t.Fatalf("exists = %v, %v", ok, err)
		}
	})

	t.Run("delete removes; deleting absent is fine", func(t *testing.T) {
		c := open(t)
		if err := c.Put(ctx, "feed", "k", []byte(`1`), 0); err != nil {
			t.Fatal(err)
		}
		if err := c.Delete(ctx, "feed", "k"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Get(ctx, "feed", "k"); !errors.Is(err, backends.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if err := c.Delete(ctx, "feed", "k"); err != nil {
			t.Fatalf("second delete: %v", err)
		}
	})

	t.Run("entries expire after their ttl", func(t *testing.T) {
		c := open(t)
		if err := c.Put(ctx, "feed", "k", []byte(`1`), 40*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Get(ctx, "feed", "k"); err != nil {
			t.Fatalf("before expiry: %v", err)
		}
		time.Sleep(60 * time.Millisecond)
		if _, err := c.Get(ctx, "feed", "k"); !errors.Is(err, backends.ErrNotFound) {
			t.Fatalf("after expiry err = %v, want ErrNotFound", err)
		}
		if ok, _ := c.Exists(ctx, "feed", "k"); ok {
			t.Fatal("expired entry still Exists")
		}
	})

	t.Run("zero ttl never expires", func(t *testing.T) {
		c := open(t)
		if err := c.Put(ctx, "feed", "k", []byte(`1`), 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
		if _, err := c.Get(ctx, "feed", "k"); err != nil {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("overwrite refreshes value and ttl", func(t *testing.T) {
		c := open(t)
		if err := c.Put(ctx, "feed", "k", []byte(`old`), 40*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if err := c.Put(ctx, "feed", "k", []byte(`new`), 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
		got, err := c.Get(ctx, "feed", "k")
		if err != nil || string(got) != "new" {
			t.Fatalf("got = %s, %v", got, err)
		}
	})

	t.Run("caches are isolated by name", func(t *testing.T) {
		c := open(t)
		if err := c.Put(ctx, "feed", "k", []byte(`feed`), 0); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Get(ctx, "sessions", "k"); !errors.Is(err, backends.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}
