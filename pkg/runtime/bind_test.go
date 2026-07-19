package runtime

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Decode error texts are 400 bodies — product UX, asserted exactly.
func TestBindErrorTexts(t *testing.T) {
	t.Run("path int", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/photos/abc", nil)
		r.SetPathValue("id", "abc")
		_, err := PathInt(r, "id")
		want := `path "id": "abc" is not an int`
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
	})
	t.Run("query bool", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x?loud=banana", nil)
		_, err := QueryBool(r, "loud")
		want := `query "loud": "banana" is not a bool`
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
	})
	t.Run("query float", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x?score=high", nil)
		_, err := QueryFloat64(r, "score")
		want := `query "score": "high" is not a number`
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
	})
	t.Run("form int64", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/x", strings.NewReader("n=abc"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		_, err := FormInt64(r, "n")
		want := `form "n": "abc" is not an int`
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
	})
	t.Run("bad json body", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/x", strings.NewReader("{oops"))
		var v struct{}
		err := DecodeJSON(r, &v)
		want := "body: invalid JSON"
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %s", err, want)
		}
	})
	t.Run("absent values are zero, not errors", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x", nil)
		if v, err := QueryBool(r, "loud"); err != nil || v {
			t.Fatalf("absent bool = %v, %v", v, err)
		}
		if v, err := QueryInt(r, "n"); err != nil || v != 0 {
			t.Fatalf("absent int = %v, %v", v, err)
		}
		var body struct{ X string }
		if err := DecodeJSON(r, &body); err != nil || body.X != "" {
			t.Fatalf("empty body = %+v, %v", body, err)
		}
	})
}

// Decode errors carry status 400; handler errors keep their own status.
func TestBindErrorStatus(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?loud=banana", nil)
	_, err := QueryBool(r, "loud")
	he, ok := err.(interface{ HTTPStatus() int })
	if !ok || he.HTTPStatus() != 400 {
		t.Fatalf("decode error status = %v", err)
	}
}
