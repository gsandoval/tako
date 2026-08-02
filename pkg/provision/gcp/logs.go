package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// LogEntry is one rendered log line's raw material.
type LogEntry struct {
	Time     time.Time
	Service  string
	Severity string
	Text     string
}

// Logs fetches recent Cloud Run logs, oldest first. service filters to
// one service; empty means every service carrying the app's labels
// (Cloud Logging is queried by service name — label filtering happens
// on the resource, so callers pass the service names they own).
func (c *Client) Logs(ctx context.Context, services []string, since time.Duration, limit int) ([]LogEntry, error) {
	filter := fmt.Sprintf(`resource.type="cloud_run_revision" AND timestamp>=%q`,
		time.Now().Add(-since).UTC().Format(time.RFC3339))
	if len(services) == 1 {
		filter += fmt.Sprintf(` AND resource.labels.service_name=%q`, services[0])
	} else if len(services) > 1 {
		filter += ` AND (`
		for i, s := range services {
			if i > 0 {
				filter += " OR "
			}
			filter += fmt.Sprintf(`resource.labels.service_name=%q`, s)
		}
		filter += `)`
	}
	body := map[string]any{
		"resourceNames": []string{"projects/" + c.Project},
		"filter":        filter,
		"orderBy":       "timestamp desc",
		"pageSize":      limit,
	}
	var out struct {
		Entries []struct {
			Timestamp   time.Time      `json:"timestamp"`
			Severity    string         `json:"severity"`
			TextPayload string         `json:"textPayload"`
			JSONPayload map[string]any `json:"jsonPayload"`
			// Cloud Run's frontend writes one request entry per request,
			// and its content lives HERE rather than in a payload — so
			// without this these arrived as blank lines.
			HTTPRequest struct {
				RequestMethod string `json:"requestMethod"`
				RequestURL    string `json:"requestUrl"`
				Status        int    `json:"status"`
				Latency       string `json:"latency"`
			} `json:"httpRequest"`
			Resource struct {
				Labels map[string]string `json:"labels"`
			} `json:"resource"`
		} `json:"entries"`
	}
	if err := c.do(ctx, http.MethodPost, c.url(loggingHost, "/v2/entries:list"), body, &out); err != nil {
		return nil, Classify(err, c.Project, "read logs", "roles/logging.viewer")
	}
	entries := make([]LogEntry, 0, len(out.Entries))
	for i := len(out.Entries) - 1; i >= 0; i-- { // oldest first
		e := out.Entries[i]
		text := e.TextPayload
		if text == "" && e.JSONPayload != nil {
			if msg, ok := e.JSONPayload["message"].(string); ok {
				text = msg
			} else {
				text = fmt.Sprint(e.JSONPayload)
			}
		}
		if text == "" && e.HTTPRequest.RequestMethod != "" {
			text = requestLine(e.HTTPRequest.RequestMethod, e.HTTPRequest.RequestURL,
				e.HTTPRequest.Status, e.HTTPRequest.Latency)
		}
		entries = append(entries, LogEntry{
			Time:     e.Timestamp,
			Service:  e.Resource.Labels["service_name"],
			Severity: e.Severity,
			Text:     text,
		})
	}
	return entries, nil
}

// requestLine renders Cloud Run's own request entry the way the dev
// console renders a local one, so `tako logs` reads the same in both
// places: METHOD /path → status (12.3ms).
func requestLine(method, rawURL string, status int, latency string) string {
	path := rawURL
	// The platform logs an absolute URL; the path is what identifies the
	// route, and the host is the same for every line anyway.
	if i := strings.Index(path, "://"); i >= 0 {
		if j := strings.Index(path[i+3:], "/"); j >= 0 {
			path = path[i+3+j:]
		}
	}
	line := fmt.Sprintf("%s %s → %d", method, path, status)
	if d, err := time.ParseDuration(latency); err == nil && d > 0 {
		line += fmt.Sprintf(" (%.1fms)", float64(d.Microseconds())/1000)
	}
	return line
}
