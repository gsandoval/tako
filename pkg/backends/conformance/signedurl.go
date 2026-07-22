package conformance

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// SignedURLEnv is one wired signed-URL setup: the signer minting URLs,
// a client that can execute them, and the backing bucket + topic to
// observe effects. Locally the executor is the /_tako/signed endpoint;
// in the cloud it is GCS itself (Phase F).
type SignedURLEnv struct {
	Signer backends.URLSigner
	Client *http.Client
	Bucket backends.Bucket
	Topic  backends.Topic
}

// RunSignedURL runs the signed-URL contract: minted URLs transfer bytes
// directly, constraints and expiry reject at transfer time (leaving no
// object behind), and a signed upload fires the bucket's upload event
// like any Put. Exact rejection texts are local product UX and are
// asserted in example tests, not here — GCS words its own refusals.
func RunSignedURL(t *testing.T, open func(t *testing.T) SignedURLEnv) {
	ctx := context.Background()
	const wait = 10 * time.Second

	put := func(t *testing.T, env SignedURLEnv, url, contentType, body string, length int64) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", contentType)
		req.ContentLength = length
		res, err := env.Client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	mustAbsent := func(t *testing.T, env SignedURLEnv, key string) {
		t.Helper()
		if ok, err := env.Bucket.Exists(ctx, "photos", key); err != nil || ok {
			t.Fatalf("object %q after rejected upload: exists=%v err=%v", key, ok, err)
		}
	}

	t.Run("signed put stores the object and fires the upload event", func(t *testing.T) {
		env := open(t)
		events := make(chan []byte, 1)
		env.Topic.Subscribe("photos.Uploaded", "probe", func(ctx context.Context, p []byte) error {
			events <- p
			return nil
		})
		url, err := env.Signer.SignPut(ctx, "photos", "cat", time.Minute, backends.SignConstraints{})
		if err != nil {
			t.Fatal(err)
		}
		if status := put(t, env, url, "image/jpeg", "JPEGBYTES", 9); status/100 != 2 {
			t.Fatalf("put status = %d", status)
		}
		got, err := env.Bucket.Get(ctx, "photos", "cat")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(got.Reader)
		if string(raw) != "JPEGBYTES" || got.ContentType != "image/jpeg" {
			t.Fatalf("stored = %q %q", raw, got.ContentType)
		}
		select {
		case <-events:
		case <-time.After(wait):
			t.Fatal("signed upload fired no event")
		}
	})

	t.Run("size cap rejects declared length", func(t *testing.T) {
		env := open(t)
		url, _ := env.Signer.SignPut(ctx, "photos", "big", time.Minute, backends.SignConstraints{MaxSize: 8})
		if status := put(t, env, url, "image/jpeg", "0123456789", 10); status/100 == 2 {
			t.Fatal("oversized upload accepted")
		}
		mustAbsent(t, env, "big")
	})

	t.Run("size cap rejects a lying stream", func(t *testing.T) {
		env := open(t)
		url, _ := env.Signer.SignPut(ctx, "photos", "liar", time.Minute, backends.SignConstraints{MaxSize: 8})
		if status := put(t, env, url, "image/jpeg", "0123456789", -1); status/100 == 2 {
			t.Fatal("oversized stream accepted")
		}
		mustAbsent(t, env, "liar")
	})

	t.Run("content type constraint rejects mismatches", func(t *testing.T) {
		env := open(t)
		url, _ := env.Signer.SignPut(ctx, "photos", "doc", time.Minute, backends.SignConstraints{ContentType: "image/*"})
		if status := put(t, env, url, "text/plain", "notes", 5); status/100 == 2 {
			t.Fatal("wrong content type accepted")
		}
		mustAbsent(t, env, "doc")
	})

	t.Run("expired URLs reject", func(t *testing.T) {
		env := open(t)
		url, _ := env.Signer.SignPut(ctx, "photos", "late", -time.Second, backends.SignConstraints{})
		if status := put(t, env, url, "image/jpeg", "x", 1); status/100 == 2 {
			t.Fatal("expired URL accepted")
		}
		mustAbsent(t, env, "late")
	})

	t.Run("tampered URLs reject", func(t *testing.T) {
		env := open(t)
		url, _ := env.Signer.SignPut(ctx, "photos", "forged", time.Minute, backends.SignConstraints{})
		if status := put(t, env, url+"x", "image/jpeg", "x", 1); status/100 == 2 {
			t.Fatal("tampered URL accepted")
		}
		mustAbsent(t, env, "forged")
	})

	t.Run("cross-origin transfers are allowed", func(t *testing.T) {
		env := open(t)
		url, err := env.Signer.SignPut(ctx, "photos", "cors", time.Minute, backends.SignConstraints{})
		if err != nil {
			t.Fatal(err)
		}
		pre, err := http.NewRequest(http.MethodOptions, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		pre.Header.Set("Origin", "http://spa.example")
		pre.Header.Set("Access-Control-Request-Method", http.MethodPut)
		res, err := env.Client.Do(pre)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 || res.Header.Get("Access-Control-Allow-Origin") == "" {
			t.Fatalf("preflight = %d, allow-origin = %q", res.StatusCode, res.Header.Get("Access-Control-Allow-Origin"))
		}
		if code := put(t, env, url, "text/plain", "X", 1); code != 200 {
			t.Fatalf("put after preflight = %d", code)
		}
	})

	t.Run("signed get streams the object", func(t *testing.T) {
		env := open(t)
		if err := env.Bucket.Put(ctx, "photos", "dl",
			backends.BlobData{Reader: strings.NewReader("BYTES"), ContentType: "image/png", Size: 5}); err != nil {
			t.Fatal(err)
		}
		url, err := env.Signer.SignGet(ctx, "photos", "dl", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		res, err := env.Client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || string(body) != "BYTES" || res.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("get = %d %q %q", res.StatusCode, body, res.Header.Get("Content-Type"))
		}

		absent, _ := env.Signer.SignGet(ctx, "photos", "ghost", time.Minute)
		res2, err := env.Client.Get(absent)
		if err != nil {
			t.Fatal(err)
		}
		defer res2.Body.Close()
		if res2.StatusCode/100 == 2 {
			t.Fatal("signed get of absent object succeeded")
		}
	})
}
