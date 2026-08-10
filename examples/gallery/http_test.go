package gallery

// The HTTP and on-disk suites: contracts whose surface IS the wire or
// the filesystem — multipart decode, signed-URL semantics and rejection
// texts, dead-letter layout under .tako-style data dirs. These stay on
// the runtime harness deliberately; app logic lives in app_test.go on
// tako.NewTestApp.

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/pkg/runtime"
)

// serve is the app's real HTTP surface (AB2): the generated mounts, the
// generated binders, the signed-URL endpoint, and every declared route.
//
// It replaced 60 lines that hand-mirrored codegen — and the mirror was
// already two routes short: POST /api/reindex and GET /api/reindex/runs
// were declared, deployed, and driven by no test here, because nobody
// noticed they had never been added.
//
// The app comes back with the server because async work needs a
// barrier, and under NewTestApp that barrier is the app's.
func serve(t *testing.T) (*tako.TestApp, *httptest.Server) {
	t.Helper()
	app := tako.NewTestApp(t)
	// BootLocal read .env; NewTestApp does not, deliberately — a test
	// says what it depends on. NotifyOwner reads this one, and the
	// subscriber runs on the same app.
	app.SetSecret("notifier-key", "nk_test_123")
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return app, srv
}

// registerHandlers is the async wiring for the tests below that boot
// their OWN data dir — the dead-letter suites, which glob the directory
// and therefore have to know it. tako.NewTestApp wires async handlers
// itself, so serve() above needs none of this; these do.
func registerHandlers(t *testing.T) {
	t.Helper()
	runtime.RegisterSubscriber("OnUploaded", OnUploaded)
	runtime.RegisterWorker("MakeThumb", MakeThumb)
	runtime.RegisterSubscriber("NotifyOwner", NotifyOwner)
	runtime.RegisterSubscriber("WarmFeed", WarmFeed)
	runtime.RegisterCron("Cleanup", Cleanup)
}

// signedPath rebases a minted signed URL onto the test server (the local
// signer stamps the PORT-derived base; only the token path matters).
func signedPath(t *testing.T, srv *httptest.Server, signed string) string {
	t.Helper()
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	return srv.URL + u.Path
}

func doReq(t *testing.T, req *http.Request) (int, string) {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, strings.TrimSpace(string(body))
}

func multipartBody(t *testing.T, caption, filename string, content []byte, contentType string) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("caption", caption); err != nil {
		t.Fatal(err)
	}
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="img"; filename="` + filename + `"`}
	h["Content-Type"] = []string{contentType}
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func TestUploadDownloadRoundtrip(t *testing.T) {
	app, srv := serve(t)
	src := pngBytes(t)

	body, ct := multipartBody(t, "cat", "cat.png", src, "image/png")
	res, err := srv.Client().Post(srv.URL+"/api/photos", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("upload status = %d: %s", res.StatusCode, raw)
	}
	var p Photo
	if err := json.NewDecoder(res.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Caption != "cat" || len(p.ID) != 16 {
		t.Fatalf("photo = %+v", p)
	}

	// Download streams the exact original bytes with the part's type.
	img, err := srv.Client().Get(srv.URL + "/api/photos/" + p.ID + "/image")
	if err != nil {
		t.Fatal(err)
	}
	defer img.Body.Close()
	got, _ := io.ReadAll(img.Body)
	if !bytes.Equal(got, src) {
		t.Fatalf("image bytes differ: got %d, want %d", len(got), len(src))
	}
	if imgCT := img.Header.Get("Content-Type"); imgCT != "image/png" {
		t.Fatalf("content type = %q", imgCT)
	}
	// These bytes were uploaded by someone else and are handed back
	// under a type this app recorded, so the browser must not go
	// looking for a better answer than the one it was given.
	if got := img.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("served image nosniff = %q", got)
	}
	// The original declares no MaxAge, so it answers no-cache — the
	// posture every blob had before Route.MaxAge reached them.
	if got := img.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("image cache-control = %q, want no-cache", got)
	}

	// Objects lists the original and (after the worker drains) its thumb —
	// a real resized PNG now, not a copy.
	app.DrainQueues()

	// The thumbnail DOES declare MaxAge, and the header is the half the
	// app cannot build itself: derived from bytes that never change,
	// requested once per photo per scroll.
	thumbRes, err := srv.Client().Get(srv.URL + "/api/photos/" + p.ID + "/thumb")
	if err != nil {
		t.Fatal(err)
	}
	thumbRes.Body.Close()
	if thumbRes.StatusCode != 200 {
		t.Fatalf("thumb = %d", thumbRes.StatusCode)
	}
	if got := thumbRes.Header.Get("Cache-Control"); got != "public, max-age=86400" {
		t.Fatalf("thumb cache-control = %q, want public, max-age=86400", got)
	}
	if got := thumbRes.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("thumb nosniff = %q", got)
	}
	objRes, err := srv.Client().Get(srv.URL + "/api/objects")
	if err != nil {
		t.Fatal(err)
	}
	defer objRes.Body.Close()
	var objs []tako.ObjectInfo
	if err := json.NewDecoder(objRes.Body).Decode(&objs); err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || objs[0].Key != p.ID || objs[1].Key != p.ID+"_thumb" ||
		objs[0].Size != int64(len(src)) || objs[1].ContentType != "image/png" {
		t.Fatalf("objects = %+v", objs)
	}
}

func TestMissingImageIs404(t *testing.T) {
	_, srv := serve(t)
	res, err := srv.Client().Get(srv.URL + "/api/photos/nope/image")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 404 || strings.TrimSpace(string(body)) != `{"error":"not found"}` {
		t.Fatalf("res = %d %s", res.StatusCode, body)
	}
}

func TestMissingUploadFieldIs400(t *testing.T) {
	_, srv := serve(t)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("caption", "cat")
	_ = w.Close()
	res, err := srv.Client().Post(srv.URL+"/api/photos", w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 400 || strings.TrimSpace(string(body)) != `{"error":"form \"img\": missing file upload"}` {
		t.Fatalf("res = %d %s", res.StatusCode, body)
	}
}

func TestNonImageDeadLetters(t *testing.T) {
	dir := t.TempDir()
	if err := runtime.BootLocal(dir); err != nil {
		t.Fatal(err)
	}
	registerHandlers(t)
	ctx := runtime.NewCtx(t.Context())

	// The bucket write alone starts the pipeline: its event enqueues the
	// ThumbJob — nothing publishes by hand anymore.
	if err := photoStore.Put(ctx, Photo{ID: "doc"}); err != nil {
		t.Fatal(err)
	}
	txt := tako.Blob{Reader: strings.NewReader("notes"), ContentType: "text/plain", Size: 5}
	if err := photos.Put(ctx, "doc", txt); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WaitIdle(30 * time.Second); err != nil {
		t.Fatal(err)
	}

	letters, err := filepath.Glob(filepath.Join(dir, "deadletter", "thumbnails", "*.json"))
	if err != nil || len(letters) != 1 {
		t.Fatalf("dead letters = %v, %v", letters, err)
	}
	raw, _ := os.ReadFile(letters[0])
	var dl struct {
		Attempts  int    `json:"attempts"`
		LastError string `json:"lastError"`
		Permanent bool   `json:"permanent"`
	}
	if err := json.Unmarshal(raw, &dl); err != nil {
		t.Fatal(err)
	}
	if dl.Attempts != 5 || dl.Permanent || dl.LastError != `unsupported content type "text/plain"` {
		t.Fatalf("dead letter = %+v", dl)
	}
	// The photo never became ready.
	p, _ := photoStore.Get(ctx, "doc")
	if p.Ready {
		t.Fatal("photo marked ready despite dead-letter")
	}
}

// TestBadImageBytesArePermanent: an image/* content type with bytes that
// don't decode gives up immediately — resize failures never retry.
func TestBadImageBytesArePermanent(t *testing.T) {
	dir := t.TempDir()
	if err := runtime.BootLocal(dir); err != nil {
		t.Fatal(err)
	}
	registerHandlers(t)
	ctx := runtime.NewCtx(t.Context())

	junk := tako.Blob{Reader: strings.NewReader("NOTANIMAGE"), ContentType: "image/jpeg", Size: 10}
	if err := photos.Put(ctx, "junk", junk); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WaitIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	letters, _ := filepath.Glob(filepath.Join(dir, "deadletter", "thumbnails", "*.json"))
	if len(letters) != 1 {
		t.Fatalf("dead letters = %v", letters)
	}
	var dl struct {
		Attempts  int    `json:"attempts"`
		LastError string `json:"lastError"`
		Permanent bool   `json:"permanent"`
	}
	raw, _ := os.ReadFile(letters[0])
	_ = json.Unmarshal(raw, &dl)
	if dl.Attempts != 1 || !dl.Permanent || !strings.HasPrefix(dl.LastError, "decode image: ") {
		t.Fatalf("dead letter = %+v", dl)
	}
}

func TestEmptyUploadIsPermanent(t *testing.T) {
	dir := t.TempDir()
	if err := runtime.BootLocal(dir); err != nil {
		t.Fatal(err)
	}
	registerHandlers(t)
	ctx := runtime.NewCtx(t.Context())

	empty := tako.Blob{Reader: strings.NewReader(""), ContentType: "image/png", Size: 0}
	if err := photos.Put(ctx, "void", empty); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WaitIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	letters, _ := filepath.Glob(filepath.Join(dir, "deadletter", "thumbnails", "*.json"))
	if len(letters) != 1 {
		t.Fatalf("dead letters = %v", letters)
	}
	var dl struct {
		Attempts  int  `json:"attempts"`
		Permanent bool `json:"permanent"`
	}
	raw, _ := os.ReadFile(letters[0])
	_ = json.Unmarshal(raw, &dl)
	if dl.Attempts != 1 || !dl.Permanent {
		t.Fatalf("dead letter = %+v", dl)
	}
}

// TestFanOutIndependence is F5's core claim: one PhotoReady event, two
// subscribers, independent fates — NotifyOwner retries into its own dead
// letter while WarmFeed acks the same event untouched.
func TestFanOutIndependence(t *testing.T) {
	dir := t.TempDir()
	if err := runtime.BootLocal(dir); err != nil {
		t.Fatal(err)
	}
	registerHandlers(t)
	ctx := runtime.NewCtx(t.Context())

	if err := photoStore.Put(ctx, Photo{ID: "b1", Caption: "boom goes"}); err != nil {
		t.Fatal(err)
	}
	if err := photos.Put(ctx, "b1", pngBlob(t)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WaitIdle(30 * time.Second); err != nil {
		t.Fatal(err)
	}

	// The pipeline itself succeeded: thumb written, photo ready.
	p, err := photoStore.Get(ctx, "b1")
	if err != nil || !p.Ready {
		t.Fatalf("photo = %+v, %v", p, err)
	}

	// NotifyOwner dead-lettered in its own directory…
	letters, err := filepath.Glob(filepath.Join(dir, "deadletter", "photo-ready", "NotifyOwner", "*.json"))
	if err != nil || len(letters) != 1 {
		t.Fatalf("NotifyOwner dead letters = %v, %v", letters, err)
	}
	raw, _ := os.ReadFile(letters[0])
	var dl struct {
		Queue      string `json:"queue"`
		Subscriber string `json:"subscriber"`
		Attempts   int    `json:"attempts"`
		LastError  string `json:"lastError"`
		Permanent  bool   `json:"permanent"`
	}
	if err := json.Unmarshal(raw, &dl); err != nil {
		t.Fatal(err)
	}
	if dl.Queue != "photo-ready" || dl.Subscriber != "NotifyOwner" || dl.Attempts != 5 ||
		dl.Permanent || dl.LastError != `notification service rejected "boom goes"` {
		t.Fatalf("dead letter = %+v", dl)
	}

	// …and WarmFeed did not: no other letters exist anywhere.
	others, _ := filepath.Glob(filepath.Join(dir, "deadletter", "photo-ready", "WarmFeed", "*.json"))
	if len(others) != 0 {
		t.Fatalf("WarmFeed dead letters = %v", others)
	}
	queueLetters, _ := filepath.Glob(filepath.Join(dir, "deadletter", "thumbnails", "*.json"))
	if len(queueLetters) != 0 {
		t.Fatalf("queue dead letters = %v", queueLetters)
	}
}

// TestSignedUploadPipeline: ticket → direct PUT → the event spine runs
// unchanged off the signed upload, and back out through a signed
// download of the exact original bytes.
func TestSignedUploadPipeline(t *testing.T) {
	app, srv := serve(t)
	src := pngBytes(t)

	res, err := srv.Client().Post(srv.URL+"/api/uploads", "application/json",
		strings.NewReader(`{"caption":"cat"}`))
	if err != nil {
		t.Fatal(err)
	}
	var ticket UploadTicket
	if err := json.NewDecoder(res.Body).Decode(&ticket); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if ticket.Photo.Caption != "cat" || !strings.Contains(ticket.UploadURL, "/_tako/api/signed/") {
		t.Fatalf("ticket = %+v", ticket)
	}

	put, _ := http.NewRequest(http.MethodPut, signedPath(t, srv, ticket.UploadURL), bytes.NewReader(src))
	put.Header.Set("Content-Type", "image/png")
	for k, v := range ticket.UploadHeaders {
		put.Header.Set(k, v)
	}
	if status, body := doReq(t, put); status != 200 {
		t.Fatalf("signed put = %d %s", status, body)
	}

	// The app's barrier, not runtime.WaitIdle: that one waits on the app
	// runtime.BootLocal booted, and NewTestApp booted this one.
	app.DrainQueues()
	ctx := runtime.NewCtx(t.Context())
	p, err := photoStore.Get(ctx, ticket.Photo.ID)
	if err != nil || !p.Ready {
		t.Fatalf("photo after signed upload = %+v, %v", p, err)
	}

	// And back out through a signed download — shaped response since F9.
	dl, err := srv.Client().Get(srv.URL + "/api/photos/" + p.ID + "/download")
	if err != nil {
		t.Fatal(err)
	}
	var shaped DownloadRes
	if err := json.NewDecoder(dl.Body).Decode(&shaped); err != nil {
		t.Fatal(err)
	}
	dl.Body.Close()
	get, _ := http.NewRequest(http.MethodGet, signedPath(t, srv, shaped.URL), nil)
	status, body := doReq(t, get)
	if status != 200 || body != string(src) {
		t.Fatalf("signed get = %d (%d bytes, want %d)", status, len(body), len(src))
	}
}

// TestSignedUploadRejections: every constraint is enforced at upload
// time with its exact text.
func TestSignedUploadRejections(t *testing.T) {
	_, srv := serve(t)
	ctx := runtime.NewCtx(t.Context())

	guarded := tako.PutPolicy{ContentType: "image/*", MaxSize: 16}
	signed, err := photos.SignedPut(ctx, "guarded", time.Minute, guarded)
	if err != nil {
		t.Fatal(err)
	}
	target := signedPath(t, srv, signed)
	sizeHeader := guarded.Headers()

	t.Run("missing size header is 403 with the pointer", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPut, target, strings.NewReader("ok"))
		req.Header.Set("Content-Type", "image/jpeg")
		status, body := doReq(t, req)
		if status != 403 || !strings.Contains(body, "PutPolicy.Headers builds it") {
			t.Fatalf("res = %d %s", status, body)
		}
	})

	t.Run("wrong content type is 415", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPut, target, strings.NewReader("notes"))
		req.Header.Set("Content-Type", "text/plain")
		status, body := doReq(t, req)
		if status != 415 || body != `{"error":"content type \"text/plain\" does not match required \"image/*\""}` {
			t.Fatalf("res = %d %s", status, body)
		}
	})

	t.Run("declared oversize is 413", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPut, target, strings.NewReader("01234567890123456789"))
		req.Header.Set("Content-Type", "image/jpeg")
		for k, v := range sizeHeader {
			req.Header.Set(k, v)
		}
		status, body := doReq(t, req)
		if status != 413 || body != `{"error":"upload is 20 bytes; the signed URL allows at most 16"}` {
			t.Fatalf("res = %d %s", status, body)
		}
	})

	t.Run("lying stream is 413 and leaves no object", func(t *testing.T) {
		// Chunked transfer hides the length; the stream cap catches it.
		req, _ := http.NewRequest(http.MethodPut, target,
			io.MultiReader(strings.NewReader("0123456789"), strings.NewReader("0123456789")))
		req.Header.Set("Content-Type", "image/jpeg")
		for k, v := range sizeHeader {
			req.Header.Set(k, v)
		}
		req.ContentLength = -1
		status, body := doReq(t, req)
		if status != 413 || body != `{"error":"upload exceeds the signed URL's 16-byte limit"}` {
			t.Fatalf("res = %d %s", status, body)
		}
		if ok, _ := photos.Exists(ctx, "guarded"); ok {
			t.Fatal("partial object survived a rejected upload")
		}
	})

	t.Run("tampered token is 403", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPut, target+"x", strings.NewReader("x"))
		req.Header.Set("Content-Type", "image/jpeg")
		status, body := doReq(t, req)
		if status != 403 || body != `{"error":"invalid signature"}` {
			t.Fatalf("res = %d %s", status, body)
		}
	})

	t.Run("expired URL is 403", func(t *testing.T) {
		gone, err := photos.SignedPut(ctx, "late", -time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPut, signedPath(t, srv, gone), strings.NewReader("x"))
		req.Header.Set("Content-Type", "image/jpeg")
		status, body := doReq(t, req)
		if status != 403 || body != `{"error":"signed URL expired"}` {
			t.Fatalf("res = %d %s", status, body)
		}
	})

	t.Run("wrong verb is 405", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		status, body := doReq(t, req)
		if status != 405 || body != `{"error":"signed upload URLs accept PUT only"}` {
			t.Fatalf("res = %d %s", status, body)
		}
	})

	t.Run("preflight is allowed for browser uploads", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodOptions, target, nil)
		req.Header.Set("Origin", "http://spa.example")
		req.Header.Set("Access-Control-Request-Method", http.MethodPut)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("preflight = %d, allow-origin = %q", res.StatusCode, res.Header.Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("absent object on signed get is 404", func(t *testing.T) {
		signed, err := photos.SignedGet(ctx, "nope", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodGet, signedPath(t, srv, signed), nil)
		status, body := doReq(t, req)
		if status != 404 || body != `{"error":"not found"}` {
			t.Fatalf("res = %d %s", status, body)
		}
	})

}

// The cursor over HTTP, which is the contract a browser actually uses:
// the server hands back a string, the client sends it back as ?cursor=,
// and neither side interprets it.
//
// The JSON shape matters as much as the walk. `cursor` is omitempty, so
// its ABSENCE is how a client learns there is no next page — the same
// question the server answered, asked without a second round trip.
func TestFeedCursorRoundTripsOverHTTP(t *testing.T) {
	_, srv := serve(t)
	c := srv.Client()

	const total = 25 // more than one page of 20
	for i := 0; i < total; i++ {
		body, ct := multipartBody(t, "photo "+strconv.Itoa(i), "p.png", pngBytes(t), "image/png")
		res, err := c.Post(srv.URL+"/api/photos", ct, body)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		time.Sleep(time.Millisecond)
	}

	get := func(cursor string) (map[string]any, string) {
		t.Helper()
		u := srv.URL + "/api/photos"
		if cursor != "" {
			u += "?cursor=" + url.QueryEscape(cursor)
		}
		res, err := c.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("GET %s = %d", u, res.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		next, _ := out["cursor"].(string)
		return out, next
	}

	// This server has no DrainQueues — the pipeline runs in the
	// background and a photo is not in the feed until the worker has
	// flipped Ready. So walk until the walk sees everything, rather than
	// racing it: the first attempt legitimately sees fewer.
	var pages []map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for {
		pages = pages[:0]
		seen := 0
		cursor := ""
		for {
			page, next := get(cursor)
			pages = append(pages, page)
			list, _ := page["photos"].([]any)
			seen += len(list)
			if next == "" {
				break
			}
			cursor = next
		}
		if seen == total {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("walked %d photos over %d pages, want %d — the pipeline never settled", seen, len(pages), total)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if len(pages) != 2 {
		t.Fatalf("%d pages for %d photos at 20 per page, want 2", len(pages), total)
	}
	firstList, _ := pages[0]["photos"].([]any)
	if len(firstList) != 20 {
		t.Errorf("first page = %d photos, want a full page of 20", len(firstList))
	}
	if _, present := pages[0]["cursor"]; !present {
		t.Error("a full first page carries no cursor — 25 photos do not fit in 20")
	}
	lastList, _ := pages[1]["photos"].([]any)
	if len(lastList) != 5 {
		t.Errorf("last page = %d photos, want the remaining 5", len(lastList))
	}
	if _, present := pages[1]["cursor"]; present {
		t.Error("the last page carries a cursor key; it is omitempty so that its ABSENCE is how a client knows it is done")
	}
}

// The second FL5 instance, and the one a method-scoped fix would have
// missed: this app declares a bucket, so tako injects the signed-URL
// endpoint, and it declares a four-segment wildcard for shareable photo
// URLs. On one mux those cannot both be registered.
func TestABucketAndAWildcardRouteCanBeMountedTogether(t *testing.T) {
	shared := func() (panicked bool) {
		defer func() { panicked = recover() != nil }()
		mux := http.NewServeMux()
		mux.Handle("GET /{owner}/{album}/{photo}/{size}", http.NotFoundHandler())
		mux.Handle("/_tako/api/signed/", http.NotFoundHandler())
		return false
	}()
	if !shared {
		t.Fatal("one shared mux accepted the signed-URL mount beside a four-segment wildcard — " +
			"the hazard this example exists to pin is gone")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("gallery cannot boot: %v", r)
		}
	}()
	takoMux := http.NewServeMux()
	takoMux.Handle("/_tako/api/signed/", http.NotFoundHandler())
	takoMux.Handle("POST /_tako/api/cron/{cron}", http.NotFoundHandler())
	app := http.NewServeMux()
	app.Handle("GET /{owner}/{album}/{photo}/{size}", http.NotFoundHandler())
	app.Handle("GET /api/photos", http.NotFoundHandler())
	runtime.Reserved(takoMux, app)
}

// The permalink resolves a photo, and refuses one that is not the
// owner's — so the route is real rather than a shape declared to prove
// a point.
func TestThePermalinkResolvesAPhotoForItsOwner(t *testing.T) {
	_, srv := serve(t)
	ctx := runtime.NewCtx(t.Context())
	if err := photoStore.Put(ctx, Photo{ID: "p1", Owner: "alice", Caption: "a cat", Ready: true}); err != nil {
		t.Fatal(err)
	}

	res, err := http.Get(srv.URL + "/alice/holidays/p1/large")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("permalink → %d, want 200", res.StatusCode)
	}
	var got Photo
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "p1" || got.Caption != "a cat" {
		t.Fatalf("permalink returned %+v, want photo p1", got)
	}

	res2, err := http.Get(srv.URL + "/bob/holidays/p1/large")
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusNotFound {
		t.Errorf("another owner's permalink → %d, want 404", res2.StatusCode)
	}
}
