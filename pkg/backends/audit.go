package backends

import (
	"encoding/json"
	"sync"
	"time"
)

// AuditRecord is a structured security/compliance audit entry emitted by
// tako.Audit[E].Record.
type AuditRecord struct {
	Stream      string          `json:"stream"`
	ActorID     string          `json:"actor_id,omitempty"`
	ActorEmail  string          `json:"actor_email,omitempty"`
	CallerIP    string          `json:"caller_ip,omitempty"`
	CallerAgent string          `json:"caller_agent,omitempty"`
	DeviceID    string          `json:"device_id,omitempty"`
	TraceID     string          `json:"trace_id,omitempty"`
	Event       json.RawMessage `json:"event"`
	RecordedAt  time.Time       `json:"recorded_at"`
}

// AuditCollector captures emitted AuditRecords in memory for local runs
// and NewTestApp assertions.
type AuditCollector struct {
	mu      sync.Mutex
	records []AuditRecord
}

// NewAuditCollector constructs an empty AuditCollector.
func NewAuditCollector() *AuditCollector {
	return &AuditCollector{}
}

// Record appends r to the collector.
func (c *AuditCollector) Record(r AuditRecord) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.records = append(c.records, r)
	c.mu.Unlock()
}

// ForStream returns all recorded audit entries for stream (or all entries
// when stream is empty).
func (c *AuditCollector) ForStream(stream string) []AuditRecord {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []AuditRecord
	for _, r := range c.records {
		if stream == "" || r.Stream == stream {
			out = append(out, r)
		}
	}
	return out
}
