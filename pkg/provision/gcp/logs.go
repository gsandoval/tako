package gcp

import (
	"context"
	"fmt"
	"net/http"
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
			Resource    struct {
				Labels map[string]string `json:"labels"`
			} `json:"resource"`
		} `json:"entries"`
	}
	if err := c.do(ctx, http.MethodPost, "https://logging.googleapis.com/v2/entries:list", body, &out); err != nil {
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
		entries = append(entries, LogEntry{
			Time:     e.Timestamp,
			Service:  e.Resource.Labels["service_name"],
			Severity: e.Severity,
			Text:     text,
		})
	}
	return entries, nil
}
