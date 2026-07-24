package gcp

import (
	"strings"
	"time"
)

const (
	waitLong = 3 * time.Minute
)

// hasLabels reports whether have carries every want label.
func hasLabels(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// withHash copies labels and stamps the spec hash.
func withHash(labels map[string]string, hash string) map[string]string {
	out := map[string]string{"tako-spec-hash": hash}
	for k, v := range labels {
		out[k] = v
	}
	return out
}

// lastSegment returns the tail of a slash-separated resource name.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}
