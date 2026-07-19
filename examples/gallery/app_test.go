package gallery

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
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

	// Objects lists the stored key.
	objRes, err := srv.Client().Get(srv.URL + "/api/objects")
	if err != nil {
		t.Fatal(err)
	}
	defer objRes.Body.Close()
	var objs []tako.ObjectInfo
	if err := json.NewDecoder(objRes.Body).Decode(&objs); err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].Key != p.ID || objs[0].Size != 9 || objs[0].ContentType != "image/jpeg" {
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
