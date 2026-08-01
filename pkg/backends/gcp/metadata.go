package gcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// metadataHost is the instance metadata server every Cloud Run container
// can reach. Overridable for tests only.
var metadataHost = "http://metadata.google.internal"

// metadataValue reads one metadata path (e.g. "instance/id"). It is
// deliberately impatient: nothing tako does at boot is worth stalling a
// cold start for, and the caller has a usable fallback for every value
// it asks about.
func metadataValue(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataHost+"/computeMetadata/v1/"+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("metadata %s: HTTP %d", path, resp.StatusCode)
	}
	return strings.TrimSpace(string(body)), nil
}

// WhoAmI assembles the identity a metrics writer needs. Cloud Run names
// the service and revision in the environment but exposes neither the
// region nor an instance id there, so those two come off the metadata
// server — which is also why neither is injected at deploy time: the
// instance can already read them, and an env var would only be a second
// copy free to disagree.
//
// A missing region is fatal to export (generic_task requires location,
// and a wrong one files metrics under a region that does not exist), so
// the caller treats the error as "not on Cloud Run, skip export". A
// missing instance id is not: hostname-pid separates writers just as
// well, which is the only job task_id has.
func WhoAmI(ctx context.Context) (Identity, error) {
	who := Identity{
		Namespace: os.Getenv("TAKO_APP") + "-" + os.Getenv("TAKO_ENV"),
		Job:       os.Getenv("TAKO_SERVICE"),
		Revision:  os.Getenv("K_REVISION"),
	}
	if who.Job == "" {
		// Falls back to Cloud Run's own name for it rather than writing
		// series under an empty job label.
		who.Job = os.Getenv("K_SERVICE")
	}
	region, err := metadataValue(ctx, "instance/region")
	if err != nil {
		return Identity{}, err
	}
	// The metadata server answers with projects/<number>/regions/<region>.
	who.Location = region
	if i := strings.LastIndex(region, "/"); i >= 0 {
		who.Location = region[i+1:]
	}
	if id, err := metadataValue(ctx, "instance/id"); err == nil && id != "" {
		who.TaskID = id
	} else {
		host, _ := os.Hostname()
		who.TaskID = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	return who, nil
}
