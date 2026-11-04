package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gsandoval/tako/internal/trace"
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
	// Audit logs share the cloud_run_revision resource but describe WHO
	// DEPLOYED, not what the app did. They carry a protoPayload this
	// command has no rendering for — they arrived as blank lines — and,
	// worse, a deploy emits enough of them to crowd the page budget and
	// push the app's own logs off the end.
	filter := fmt.Sprintf(`resource.type="cloud_run_revision" AND timestamp>=%q`+
		` AND NOT logName:"cloudaudit.googleapis.com"`,
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
		if e.JSONPayload != nil && e.JSONPayload["message"] == "tako_span" {
			continue
		}
		text := e.TextPayload
		if text == "" && e.JSONPayload != nil {
			text = renderPayload(e.JSONPayload)
		}
		if text == "" && e.HTTPRequest.RequestMethod != "" {
			text = requestLine(e.HTTPRequest.RequestMethod, e.HTTPRequest.RequestURL,
				e.HTTPRequest.Status, e.HTTPRequest.Latency)
		}
		if text == "" {
			// Nothing renderable: a shape this command does not know.
			// Printing a timestamp and a severity with no message is
			// worse than printing nothing.
			continue
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

// payloadNoise are Cloud Logging's own keys, already rendered as the
// entry's columns or meaningless to a reader.
var payloadNoise = map[string]bool{
	"message": true, "severity": true, "time": true, "timestamp": true,
	"logging.googleapis.com/trace": true, "logging.googleapis.com/sourceLocation": true,
	"logging.googleapis.com/spanId": true, "logging.googleapis.com/labels": true,
}

// renderPayload turns a structured entry into what a person needs.
//
// This used to be `jsonPayload["message"]` and nothing else, which for a
// handler error printed "handler error" and dropped the err — the field
// carrying the actual failure. RC1's 500 was diagnosed with
// `gcloud logging read` for exactly that reason: Datastore's "no
// matching index found. recommended index is: …" was one query away and
// tako's own reader was hiding it.
//
// The request id comes along on the first line: it is what ties this
// entry to the request line above it, which is the correlation
// pages.md's "an opaque 500 with the request id" promises.
func renderPayload(payload map[string]any) string {
	msg, _ := payload["message"].(string)
	var b strings.Builder
	b.WriteString(msg)
	if req, ok := payload["req"].(string); ok && req != "" {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString("[req=" + req + "]")
	}
	// err first — it is why you are reading this line — then whatever
	// else the handler attached, in a stable order.
	var rest []string
	for k := range payload {
		if payloadNoise[k] || k == "req" || k == "err" {
			continue
		}
		rest = append(rest, k)
	}
	sort.Strings(rest)
	if _, ok := payload["err"]; ok {
		rest = append([]string{"err"}, rest...)
	}
	for _, k := range rest {
		v := fmt.Sprint(payload[k])
		if v == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		// A multi-line value (Datastore's index recommendation is a YAML
		// block) keeps its shape, indented under its key rather than
		// folded into one unreadable line.
		lines := strings.Split(strings.TrimRight(v, "\n"), "\n")
		b.WriteString(k + ": " + lines[0])
		for _, l := range lines[1:] {
			// The value's OWN indentation is kept — Datastore's
			// recommendation is nested YAML, and flattening it throws
			// away the part that makes it copyable.
			b.WriteString("\n" + strings.Repeat(" ", len(k)+2) + l)
		}
	}
	return b.String()
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

// Trace fetches spans and log entries belonging to a single distributed trace
// or Tako request ID across the app's services.
func (c *Client) Trace(ctx context.Context, services []string, id string, since time.Duration) ([]trace.Span, []LogEntry, error) {
	id = strings.TrimSpace(id)
	filter := fmt.Sprintf(`(resource.type="cloud_run_revision" OR resource.type="cloud_run_job") AND timestamp>=%q`+
		` AND NOT logName:"cloudaudit.googleapis.com"`+
		` AND (trace="projects/%s/traces/%s" OR jsonPayload.req=%q OR jsonPayload.span.trace_id=%q OR jsonPayload.span.req_id=%q)`,
		time.Now().Add(-since).UTC().Format(time.RFC3339),
		c.Project, id, id, id, id)
	if len(services) == 1 {
		filter += fmt.Sprintf(` AND (resource.labels.service_name=%q OR resource.labels.job_name=%q)`, services[0], services[0])
	} else if len(services) > 1 {
		filter += ` AND (`
		for i, s := range services {
			if i > 0 {
				filter += " OR "
			}
			filter += fmt.Sprintf(`resource.labels.service_name=%q OR resource.labels.job_name=%q`, s, s)
		}
		filter += `)`
	}
	body := map[string]any{
		"resourceNames": []string{"projects/" + c.Project},
		"filter":        filter,
		"orderBy":       "timestamp asc",
		"pageSize":      500,
	}
	var out struct {
		Entries []struct {
			Timestamp   time.Time      `json:"timestamp"`
			Severity    string         `json:"severity"`
			TextPayload string         `json:"textPayload"`
			JSONPayload map[string]any `json:"jsonPayload"`
			Trace       string         `json:"trace"`
			SpanID      string         `json:"spanId"`
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
		return nil, nil, Classify(err, c.Project, "read trace logs", "roles/logging.viewer")
	}
	var spans []trace.Span
	var logs []LogEntry
	for _, e := range out.Entries {
		svc := e.Resource.Labels["service_name"]
		if svc == "" {
			svc = e.Resource.Labels["job_name"]
		}
		if e.JSONPayload != nil && e.JSONPayload["message"] == "tako_span" {
			if rawSpan, ok := e.JSONPayload["span"]; ok {
				rawBytes, err := json.Marshal(rawSpan)
				if err == nil {
					var sp trace.Span
					if json.Unmarshal(rawBytes, &sp) == nil {
						if sp.Service == "" {
							sp.Service = svc
						}
						spans = append(spans, sp)
						continue
					}
				}
			}
		}
		text := e.TextPayload
		if text == "" && e.JSONPayload != nil {
			text = renderPayload(e.JSONPayload)
		}
		if text == "" && e.HTTPRequest.RequestMethod != "" {
			text = requestLine(e.HTTPRequest.RequestMethod, e.HTTPRequest.RequestURL,
				e.HTTPRequest.Status, e.HTTPRequest.Latency)
		}
		if text == "" {
			continue
		}
		logs = append(logs, LogEntry{
			Time:     e.Timestamp,
			Service:  svc,
			Severity: e.Severity,
			Text:     text,
		})
	}
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].StartTime.Before(spans[j].StartTime)
	})
	return spans, logs, nil
}

// AuditLogEntry is a parsed application or Cloud Audit Log record.
type AuditLogEntry struct {
	Time     time.Time `json:"time"`
	Stream   string    `json:"stream"`
	Actor    string    `json:"actor"`
	CallerIP string    `json:"caller_ip"`
	Resource string    `json:"resource,omitempty"`
	Summary  string    `json:"summary"`
}

// AuditLogs queries either application audit records emitted by tako.Audit[E]
// (when cloudOnly is false) or native Google Cloud Audit Logs
// (cloudaudit.googleapis.com, when cloudOnly is true).
func (c *Client) AuditLogs(ctx context.Context, stream, actor string, cloudOnly bool, since time.Duration) ([]AuditLogEntry, error) {
	cutoff := time.Now().Add(-since).UTC().Format(time.RFC3339)
	var filter string
	if cloudOnly {
		filter = fmt.Sprintf(`timestamp >= %q AND logName:"cloudaudit.googleapis.com"`, cutoff)
		if actor != "" {
			filter += fmt.Sprintf(` AND protoPayload.authenticationInfo.principalEmail=%q`, actor)
		}
	} else {
		filter = fmt.Sprintf(`timestamp >= %q`, cutoff)
		if stream != "" {
			filter += fmt.Sprintf(` AND (labels."tako.audit"=%q OR jsonPayload.auditStream=%q)`, stream, stream)
		} else {
			filter += ` AND (labels."tako.audit":* OR jsonPayload.auditStream:*)`
		}
		if actor != "" {
			filter += fmt.Sprintf(` AND (labels."tako.actor"=%q OR jsonPayload.authenticationInfo.principalSubject=%q OR jsonPayload.authenticationInfo.principalEmail=%q)`,
				actor, actor, actor)
		}
	}

	body := map[string]any{
		"resourceNames": []string{"projects/" + c.Project},
		"filter":        filter,
		"orderBy":       "timestamp asc",
		"pageSize":      200,
	}
	var out struct {
		Entries []struct {
			Timestamp   time.Time         `json:"timestamp"`
			Labels      map[string]string `json:"labels"`
			JSONPayload map[string]any    `json:"jsonPayload"`
			ProtoPayload struct {
				ServiceName        string `json:"serviceName"`
				MethodName         string `json:"methodName"`
				ResourceName       string `json:"resourceName"`
				AuthenticationInfo struct {
					PrincipalEmail   string `json:"principalEmail"`
					PrincipalSubject string `json:"principalSubject"`
				} `json:"authenticationInfo"`
				RequestMetadata struct {
					CallerIP string `json:"callerIp"`
				} `json:"requestMetadata"`
			} `json:"protoPayload"`
		} `json:"entries"`
	}
	if err := c.do(ctx, http.MethodPost, c.url(loggingHost, "/v2/entries:list"), body, &out); err != nil {
		return nil, Classify(err, c.Project, "read audit logs", "roles/logging.viewer")
	}

	var entries []AuditLogEntry
	for _, e := range out.Entries {
		if cloudOnly {
			who := e.ProtoPayload.AuthenticationInfo.PrincipalEmail
			if who == "" {
				who = e.ProtoPayload.AuthenticationInfo.PrincipalSubject
			}
			entries = append(entries, AuditLogEntry{
				Time:     e.Timestamp,
				Stream:   e.ProtoPayload.ServiceName,
				Actor:    who,
				CallerIP: e.ProtoPayload.RequestMetadata.CallerIP,
				Resource: e.ProtoPayload.ResourceName,
				Summary:  e.ProtoPayload.MethodName,
			})
			continue
		}
		st := e.Labels["tako.audit"]
		if st == "" && e.JSONPayload != nil {
			st, _ = e.JSONPayload["auditStream"].(string)
		}
		var who, ip string
		if e.JSONPayload != nil {
			if ai, ok := e.JSONPayload["authenticationInfo"].(map[string]any); ok {
				who, _ = ai["principalEmail"].(string)
				if who == "" {
					who, _ = ai["principalSubject"].(string)
				}
			}
			if rm, ok := e.JSONPayload["requestMetadata"].(map[string]any); ok {
				ip, _ = rm["callerIp"].(string)
			}
		}
		if who == "" {
			who = e.Labels["tako.actor"]
		}
		var summary string
		if e.JSONPayload != nil {
			if ev, ok := e.JSONPayload["auditEvent"]; ok {
				raw, _ := json.Marshal(ev)
				summary = string(raw)
			}
		}
		entries = append(entries, AuditLogEntry{
			Time:     e.Timestamp,
			Stream:   st,
			Actor:    who,
			CallerIP: ip,
			Summary:  summary,
		})
	}
	return entries, nil
}
