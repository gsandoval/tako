package gallery

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
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako"
	"github.com/gsandoval/tako/pkg/runtime"
)

// serve mirrors the generated service's mux, including the generated
// decode logic. (tako.NewTestApp replaces this plumbing in F7.)
func serve(t *testing.T) *httptest.Server {
	t.Helper()
	if err := runtime.BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	registerHandlers(t)
	mux := http.NewServeMux()
	mux.Handle("POST /api/photos", runtime.JSONHandler(Upload, func(r *http.Request) (UploadReq, error) {
		var req UploadReq
		var err error
		req.Caption = runtime.FormString(r, "caption")
		if req.Img, err = runtime.FormBlob(r, "img"); err != nil {
			return req, err
		}
		return req, nil
	}))
	mux.Handle("GET /api/photos/{id}/image", runtime.BlobHandler(Image, func(r *http.Request) (ImageReq, error) {
		var req ImageReq
		req.ID = runtime.PathString(r, "id")
		return req, nil
	}))
	mux.Handle("GET /api/objects", runtime.JSONHandler(Objects, nil))
	mux.Handle("POST /api/uploads", runtime.JSONHandler(StartUpload, func(r *http.Request) (StartUploadReq, error) {
		var req StartUploadReq
		if err := runtime.DecodeJSON(r, &req); err != nil {
			return req, err
		}
		return req, nil
	}))
	mux.Handle("GET /api/photos/{id}/download", runtime.JSONHandler(Download, func(r *http.Request) (DownloadReq, error) {
		var req DownloadReq
		req.ID = runtime.PathString(r, "id")
		return req, nil
	}))
	mux.Handle("/_tako/signed/", runtime.SignedURLHandler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
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

func multipartBody(t *testing.T, caption, filename, content, contentType string) (io.Reader, string) {
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
	if _, err := io.WriteString(part, content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func TestUploadDownloadRoundtrip(t *testing.T) {
	srv := serve(t)

	body, ct := multipartBody(t, "cat", "cat.jpg", "JPEGBYTES", "image/jpeg")
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

	// Download streams the exact bytes with the part's content type.
	img, err := srv.Client().Get(srv.URL + "/api/photos/" + p.ID + "/image")
	if err != nil {
		t.Fatal(err)
	}
	defer img.Body.Close()
	got, _ := io.ReadAll(img.Body)
	if string(got) != "JPEGBYTES" {
		t.Fatalf("bytes = %q", got)
	}
	if imgCT := img.Header.Get("Content-Type"); imgCT != "image/jpeg" {
		t.Fatalf("content type = %q", imgCT)
	}
	if cl := img.Header.Get("Content-Length"); cl != "9" {
		t.Fatalf("content length = %q", cl)
	}

	// Objects lists the original and (after the worker drains) its thumb.
	if err := runtime.WaitIdle(10 * time.Second); err != nil {
		t.Fatal(err)
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
		objs[0].Size != 9 || objs[0].ContentType != "image/jpeg" {
		t.Fatalf("objects = %+v", objs)
	}
}

func TestMissingImageIs404(t *testing.T) {
	srv := serve(t)
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
	srv := serve(t)
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

// registerHandlers mirrors the generated main's async wiring: the worker
// and all three subscribers.
func registerHandlers(t *testing.T) {
	t.Helper()
	runtime.RegisterSubscriber("OnUploaded", OnUploaded)
	runtime.RegisterWorker("MakeThumb", MakeThumb)
	runtime.RegisterSubscriber("NotifyOwner", NotifyOwner)
	runtime.RegisterSubscriber("WarmFeed", WarmFeed)
}

func TestThumbnailPipeline(t *testing.T) {
	srv := serve(t)

	body, ct := multipartBody(t, "cat", "cat.jpg", "JPEGBYTES", "image/jpeg")
	res, err := srv.Client().Post(srv.URL+"/api/photos", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	var p Photo
	if err := json.NewDecoder(res.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	if err := runtime.WaitIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}

	ctx := runtime.NewCtx(t.Context())
	got, err := photoStore.Get(ctx, p.ID)
	if err != nil || !got.Ready {
		t.Fatalf("photo after worker = %+v, %v", got, err)
	}
	tb, err := photos.Get(ctx, p.ID+"_thumb")
	if err != nil {
		t.Fatalf("thumb missing: %v", err)
	}
	raw, _ := io.ReadAll(tb.Reader)
	if string(raw) != "JPEGBYTES" {
		t.Fatalf("thumb bytes = %q", raw)
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
	img := tako.Blob{Reader: strings.NewReader("JPEGBYTES"), ContentType: "image/jpeg", Size: 9}
	if err := photos.Put(ctx, "b1", img); err != nil {
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

// TestSignedUploadPipeline: ticket → direct PUT → the F5 event spine
// runs unchanged off the signed upload.
func TestSignedUploadPipeline(t *testing.T) {
	srv := serve(t)

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
	if ticket.Photo.Caption != "cat" || !strings.Contains(ticket.UploadURL, "/_tako/signed/") {
		t.Fatalf("ticket = %+v", ticket)
	}

	put, _ := http.NewRequest(http.MethodPut, signedPath(t, srv, ticket.UploadURL),
		strings.NewReader("JPEGBYTES"))
	put.Header.Set("Content-Type", "image/jpeg")
	if status, body := doReq(t, put); status != 200 {
		t.Fatalf("signed put = %d %s", status, body)
	}

	if err := runtime.WaitIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	ctx := runtime.NewCtx(t.Context())
	p, err := photoStore.Get(ctx, ticket.Photo.ID)
	if err != nil || !p.Ready {
		t.Fatalf("photo after signed upload = %+v, %v", p, err)
	}

	// And back out through a signed download.
	dl, err := srv.Client().Get(srv.URL + "/api/photos/" + p.ID + "/download")
	if err != nil {
		t.Fatal(err)
	}
	var signedURL string
	if err := json.NewDecoder(dl.Body).Decode(&signedURL); err != nil {
		t.Fatal(err)
	}
	dl.Body.Close()
	get, _ := http.NewRequest(http.MethodGet, signedPath(t, srv, signedURL), nil)
	status, body := doReq(t, get)
	if status != 200 || body != "JPEGBYTES" {
		t.Fatalf("signed get = %d %q", status, body)
	}
}

// TestSignedUploadRejections: every constraint is enforced at upload
// time with its exact text.
func TestSignedUploadRejections(t *testing.T) {
	srv := serve(t)
	ctx := runtime.NewCtx(t.Context())

	signed, err := photos.SignedPut(ctx, "guarded", time.Minute,
		tako.WithContentType("image/*"), tako.WithMaxSize(16))
	if err != nil {
		t.Fatal(err)
	}
	target := signedPath(t, srv, signed)

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

	t.Run("wrong option errors at call time", func(t *testing.T) {
		_, err := photos.SignedPut(ctx, "k", time.Minute, tako.WithTTL(time.Minute))
		want := "tako.WithTTL is not a SignedPut option (SignedPut accepts WithMaxSize, WithContentType)"
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v", err)
		}
	})
}
