package conformance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/backends"
)

// RunBucket runs the bucket-contract suite against a fresh store from
// open.
func RunBucket(t *testing.T, open func(t *testing.T) backends.Bucket) {
	ctx := context.Background()
	blob := func(s, ct string) backends.BlobData {
		return backends.BlobData{Reader: strings.NewReader(s), ContentType: ct, Size: int64(len(s))}
	}
	read := func(t *testing.T, d backends.BlobData) string {
		t.Helper()
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, d.Reader); err != nil {
			t.Fatal(err)
		}
		if c, ok := d.Reader.(io.Closer); ok {
			_ = c.Close()
		}
		return buf.String()
	}

	// FL25: a stored object carries a validator, so a reader can ask
	// "still this one?" instead of downloading 758 KB it already has.
	//
	// The two backends cannot produce the SAME string and should not
	// pretend to — GCS hands over its own etag, local derives one from
	// size and mtime — so what the contract pins is the property that
	// makes a validator worth sending.
	t.Run("a stored object has an etag", func(t *testing.T) {
		s := open(t)
		if err := s.Put(ctx, "photos", "harvest", blob("original", "image/jpeg")); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, "photos", "harvest")
		if err != nil {
			t.Fatal(err)
		}
		read(t, got)
		if got.ETag == "" {
			t.Fatal("no ETag on a stored object — without one a blob response can only say " +
				"no-cache, which means fetch it all again rather than ask first")
		}
	})

	t.Run("rewriting an object changes its etag", func(t *testing.T) {
		s := open(t)
		if err := s.Put(ctx, "photos", "harvest", blob("original", "image/jpeg")); err != nil {
			t.Fatal(err)
		}
		first, err := s.Get(ctx, "photos", "harvest")
		if err != nil {
			t.Fatal(err)
		}
		read(t, first)

		if err := s.Put(ctx, "photos", "harvest", blob("replaced, and larger", "image/jpeg")); err != nil {
			t.Fatal(err)
		}
		second, err := s.Get(ctx, "photos", "harvest")
		if err != nil {
			t.Fatal(err)
		}
		read(t, second)

		if second.ETag == first.ETag {
			t.Fatalf("the etag survived a rewrite (%q) — a reader holding the old one would be "+
				"told 304 and keep showing the replaced image forever", first.ETag)
		}
	})

	// Deliberately NOT asserted: that re-putting identical bytes keeps the
	// etag. A GCS generation changes and local's mtime changes, so
	// requiring stability there would be a lie about both backends. A
	// validator has to change when the bytes change; it is free to change
	// at other times too, at the cost of one extra download.

	t.Run("put get roundtrip with content type", func(t *testing.T) {
		s := open(t)
		if err := s.Put(ctx, "photos", "cat", blob("meow", "image/jpeg")); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, "photos", "cat")
		if err != nil {
			t.Fatal(err)
		}
		if body := read(t, got); body != "meow" {
			t.Fatalf("body = %q", body)
		}
		if got.ContentType != "image/jpeg" || got.Size != 4 {
			t.Fatalf("meta = %q, %d", got.ContentType, got.Size)
		}
	})

	t.Run("get missing is ErrNotFound", func(t *testing.T) {
		s := open(t)
		if _, err := s.Get(ctx, "photos", "nope"); !errors.Is(err, backends.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("put overwrites", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, "photos", "cat", blob("one", "text/plain"))
		if err := s.Put(ctx, "photos", "cat", blob("twotwo", "text/html")); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, "photos", "cat")
		if body := read(t, got); body != "twotwo" || got.ContentType != "text/html" {
			t.Fatalf("after overwrite: %q, %q", body, got.ContentType)
		}
	})

	t.Run("exists and idempotent delete", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, "photos", "cat", blob("meow", "image/jpeg"))
		if ok, _ := s.Exists(ctx, "photos", "cat"); !ok {
			t.Fatal("Exists = false")
		}
		if err := s.Delete(ctx, "photos", "cat"); err != nil {
			t.Fatal(err)
		}
		if ok, _ := s.Exists(ctx, "photos", "cat"); ok {
			t.Fatal("exists after delete")
		}
		if err := s.Delete(ctx, "photos", "cat"); err != nil {
			t.Fatalf("second delete: %v", err)
		}
	})

	t.Run("list by prefix in key order", func(t *testing.T) {
		s := open(t)
		for _, k := range []string{"b/2", "a", "b/1", "c"} {
			if err := s.Put(ctx, "photos", k, blob("x", "text/plain")); err != nil {
				t.Fatal(err)
			}
		}
		all, err := s.List(ctx, "photos", "")
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, o := range all {
			keys = append(keys, o.Key)
		}
		if fmt.Sprint(keys) != "[a b/1 b/2 c]" {
			t.Fatalf("keys = %v", keys)
		}
		sub, err := s.List(ctx, "photos", "b/")
		if err != nil || len(sub) != 2 || sub[0].Key != "b/1" {
			t.Fatalf("prefix list = %v, %v", sub, err)
		}
		if sub[0].Size != 1 || sub[0].ContentType != "text/plain" || sub[0].Updated.IsZero() {
			t.Fatalf("meta = %+v", sub[0])
		}
	})

	t.Run("list of empty bucket is empty", func(t *testing.T) {
		s := open(t)
		all, err := s.List(ctx, "photos", "")
		if err != nil || len(all) != 0 {
			t.Fatalf("list = %v, %v", all, err)
		}
	})

	t.Run("buckets are isolated", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, "photos", "cat", blob("meow", "image/jpeg"))
		if ok, _ := s.Exists(ctx, "thumbs", "cat"); ok {
			t.Fatal("object leaked across buckets")
		}
	})
}
