package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2/google"
)

// Options configures the runtime backends for one deployed service.
type Options struct {
	Project string // GCP project id (TAKO_PROJECT)
	App     string // app name, scopes bucket names (TAKO_APP)
	Env     string // environment, the Datastore namespace (TAKO_ENV)
	Service string // this service's base name (TAKO_SERVICE) — dead-topic derivation
	// BucketEvents names the buckets whose writes the runtime announces
	// on their events topic (TAKO_BUCKET_EVENTS) — the buckets with
	// Uploaded subscribers, computed at deploy time.
	BucketEvents map[string]bool
}

// Client is the shared authenticated HTTP client behind the stores.
type Client struct {
	HC   *http.Client
	Opts Options
}

// New authenticates via ADC (on Cloud Run: the service's own SA).
func New(ctx context.Context, opts Options) (*Client, error) {
	hc, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("cannot authenticate to Google Cloud: %v", err)
	}
	return &Client{HC: hc, Opts: opts}, nil
}

// statusError is a non-2xx API answer. Callers translate 404s into the
// contract's ErrNotFound where absence is meaningful.
type statusError struct {
	Code int
	Body string
	URL  string
}

func (e *statusError) Error() string {
	if e.Code == http.StatusForbidden {
		return fmt.Sprintf("permission denied by %s — this service's grants derive from the app graph at deploy time, so a runtime 403 is a tako deploy bug: re-run tako deploy, and file it if it persists (%s)", e.URL, e.Body)
	}
	return fmt.Sprintf("HTTP %d from %s: %s", e.Code, e.URL, e.Body)
}

// do runs one JSON call; nil out discards the response.
func (c *Client) do(ctx context.Context, method, url string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HC.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return &statusError{Code: resp.StatusCode, Body: compact(raw), URL: req.URL.Path}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func compact(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	s := string(raw)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func isStatus(err error, code int) bool {
	se, ok := err.(*statusError)
	return ok && se.Code == code
}
