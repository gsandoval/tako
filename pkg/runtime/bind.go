package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// Request-binding helpers called by generated decode functions. Their
// error texts surface as 400 bodies and are product UX under test:
//
//	query "loud": "banana" is not a bool
//	path "id": "abc" is not an int
//	body: invalid JSON

// bindError is a decode failure; it surfaces as a 400.
type bindError struct{ msg string }

func (e *bindError) Error() string   { return e.msg }
func (e *bindError) HTTPStatus() int { return http.StatusBadRequest }

func parseErr(kind, name, raw, typ string) error {
	return &bindError{msg: fmt.Sprintf("%s %q: %q is not %s", kind, name, raw, typ)}
}

// DecodeJSON decodes the request body into req. Empty bodies decode to the
// zero value.
func DecodeJSON(r *http.Request, req any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(req); err != nil {
		if errors.Is(err, io.EOF) { // empty body: all fields stay zero
			return nil
		}
		return &bindError{msg: "body: invalid JSON"}
	}
	return nil
}

// QueryString returns the named query parameter ("" when absent).
func QueryString(r *http.Request, name string) string {
	return r.URL.Query().Get(name)
}

// QueryStrings returns EVERY value of the named query parameter (nil
// when absent). A repeated parameter is one field, not a list of
// fields, so nothing is dropped.
func QueryStrings(r *http.Request, name string) []string {
	return r.URL.Query()[name]
}

// QueryBool parses the named query parameter as a bool (false when absent).
func QueryBool(r *http.Request, name string) (bool, error) {
	return parseBool("query", name, r.URL.Query().Get(name))
}

// QueryInt parses the named query parameter as an int (0 when absent).
func QueryInt(r *http.Request, name string) (int, error) {
	return parseInt("query", name, r.URL.Query().Get(name))
}

// QueryInt64 parses the named query parameter as an int64 (0 when absent).
func QueryInt64(r *http.Request, name string) (int64, error) {
	return parseInt64("query", name, r.URL.Query().Get(name))
}

// QueryFloat64 parses the named query parameter as a float64 (0 when
// absent).
func QueryFloat64(r *http.Request, name string) (float64, error) {
	return parseFloat64("query", name, r.URL.Query().Get(name))
}

// PathString returns the named route parameter.
func PathString(r *http.Request, name string) string {
	return r.PathValue(name)
}

// PathInt parses the named route parameter as an int.
func PathInt(r *http.Request, name string) (int, error) {
	return parseInt("path", name, r.PathValue(name))
}

// PathInt64 parses the named route parameter as an int64.
func PathInt64(r *http.Request, name string) (int64, error) {
	return parseInt64("path", name, r.PathValue(name))
}

// PathFloat64 parses the named route parameter as a float64.
func PathFloat64(r *http.Request, name string) (float64, error) {
	return parseFloat64("path", name, r.PathValue(name))
}

// PathBool parses the named route parameter as a bool.
func PathBool(r *http.Request, name string) (bool, error) {
	return parseBool("path", name, r.PathValue(name))
}

// FormString returns the named form field ("" when absent).
func FormString(r *http.Request, name string) string {
	return r.FormValue(name)
}

// FormStrings returns EVERY value of the named form field (nil when
// absent). A group of checkboxes named alike is the HTML shape of
// "select some rows"; FormString would return only the first of them.
func FormStrings(r *http.Request, name string) []string {
	// FormValue parses on demand; this reads r.Form directly, so it
	// must ask for the parse itself. ParseMultipartForm parses the URL
	// query and any urlencoded body BEFORE it looks for a multipart
	// part, so a non-multipart request still populates r.Form and the
	// error it returns here is not one to act on.
	_ = r.ParseMultipartForm(maxMultipartMemory)
	return r.Form[name]
}

// FormBool parses the named form field as a bool (false when absent).
func FormBool(r *http.Request, name string) (bool, error) {
	return parseBool("form", name, r.FormValue(name))
}

// FormInt parses the named form field as an int (0 when absent).
func FormInt(r *http.Request, name string) (int, error) {
	return parseInt("form", name, r.FormValue(name))
}

// FormInt64 parses the named form field as an int64 (0 when absent).
func FormInt64(r *http.Request, name string) (int64, error) {
	return parseInt64("form", name, r.FormValue(name))
}

// FormFloat64 parses the named form field as a float64 (0 when absent).
func FormFloat64(r *http.Request, name string) (float64, error) {
	return parseFloat64("form", name, r.FormValue(name))
}

func parseBool(kind, name, raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, parseErr(kind, name, raw, "a bool")
	}
	return v, nil
}

func parseInt(kind, name, raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, parseErr(kind, name, raw, "an int")
	}
	return v, nil
}

func parseInt64(kind, name, raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, parseErr(kind, name, raw, "an int")
	}
	return v, nil
}

func parseFloat64(kind, name, raw string) (float64, error) {
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, parseErr(kind, name, raw, "a number")
	}
	return v, nil
}
