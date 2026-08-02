package gcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Lease is the deploy lease (spec §4.6a): a GCS object created with a
// generation-0 precondition in the app's framework bucket, so a CI run
// and a laptop can't interleave half-applied graphs. Stale leases
// (>10min) are broken with a warning — a crashed deploy must not brick
// the app.
type Lease struct {
	C      *Client
	App    string
	Env    string
	Labels map[string]string
	// Say prints one status line (the deploy banner's voice).
	Say func(format string, args ...any)
}

const leaseStale = 10 * time.Minute

func (l *Lease) bucket() string { return l.C.Project + "-tako" }
func (l *Lease) object() string { return "leases/deploy-" + l.App + "-" + l.Env }

// Acquire takes the lease, waiting out a live holder; the returned
// release deletes it.
func (l *Lease) Acquire(ctx context.Context, holder string) (release func(), err error) {
	if err := l.ensureBucket(ctx); err != nil {
		return nil, err
	}
	waited := false
	for {
		err := l.tryCreate(ctx, holder)
		if err == nil {
			return func() { _ = l.delete(context.WithoutCancel(ctx)) }, nil
		}
		ae, ok := err.(*apiError)
		if !ok || ae.Status != http.StatusPreconditionFailed {
			return nil, Classify(err, l.C.Project, "take the deploy lease", "roles/storage.objectAdmin")
		}
		who, since, getErr := l.holder(ctx)
		if getErr == nil && time.Since(since) > leaseStale {
			l.Say("lease        held by %s since %s — stale, breaking it", who, since.Format(time.Kitchen))
			_ = l.delete(ctx)
			continue
		}
		if !waited {
			l.Say("lease        deploy in progress by %s since %s — waiting…", who, since.Format(time.Kitchen))
			waited = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (l *Lease) ensureBucket(ctx context.Context) error {
	err := l.C.do(ctx, http.MethodGet, l.C.url(storageHost, "/storage/v1/b/"+l.bucket()), nil, nil)
	if err == nil {
		return nil
	}
	if ae, ok := err.(*apiError); !ok || ae.Status != http.StatusNotFound {
		return Classify(err, l.C.Project, "read the framework bucket", "roles/storage.admin")
	}
	body := map[string]any{"name": l.bucket(), "labels": l.Labels}
	createURL := l.C.url(storageHost, "/storage/v1/b?project="+l.C.Project)
	if err := l.C.do(ctx, http.MethodPost, createURL, body, nil); err != nil {
		return Classify(err, l.C.Project, "create the framework bucket", "roles/storage.admin")
	}
	return nil
}

func (l *Lease) tryCreate(ctx context.Context, holder string) error {
	meta := map[string]string{"holder": holder, "since": time.Now().UTC().Format(time.RFC3339)}
	raw, _ := json.Marshal(meta)
	u := l.C.url(storageHost, "/upload/storage/v1/b/%s/o?uploadType=media&name=%s&ifGenerationMatch=0",
		l.bucket(), url.QueryEscape(l.object()))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := l.C.HC.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return &apiError{Status: res.StatusCode, Message: "deploy lease exists", API: storageHost}
	}
	return nil
}

func (l *Lease) holder(ctx context.Context) (string, time.Time, error) {
	u := l.C.url(storageHost, "/storage/v1/b/%s/o/%s?alt=media", l.bucket(), url.QueryEscape(l.object()))
	var meta struct {
		Holder string    `json:"holder"`
		Since  time.Time `json:"since"`
	}
	if err := l.C.do(ctx, http.MethodGet, u, nil, &meta); err != nil {
		return "unknown", time.Time{}, err
	}
	return meta.Holder, meta.Since, nil
}

func (l *Lease) delete(ctx context.Context) error {
	u := l.C.url(storageHost, "/storage/v1/b/%s/o/%s", l.bucket(), url.QueryEscape(l.object()))
	return l.C.do(ctx, http.MethodDelete, u, nil, nil)
}

// DeleteBucket removes the framework bucket (tako destroy): objects
// first, then the bucket.
func (l *Lease) DeleteBucket(ctx context.Context) error {
	var listing struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	listURL := l.C.url(storageHost, "/storage/v1/b/%s/o", l.bucket())
	if err := l.C.do(ctx, http.MethodGet, listURL, nil, &listing); err != nil {
		if ae, ok := err.(*apiError); ok && ae.Status == http.StatusNotFound {
			return nil
		}
		return err
	}
	for _, item := range listing.Items {
		u := l.C.url(storageHost, "/storage/v1/b/%s/o/%s", l.bucket(), url.QueryEscape(item.Name))
		if err := l.C.do(ctx, http.MethodDelete, u, nil, nil); err != nil {
			return err
		}
	}
	return l.C.do(ctx, http.MethodDelete, l.C.url(storageHost, "/storage/v1/b/"+l.bucket()), nil, nil)
}
