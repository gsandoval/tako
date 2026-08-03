package gcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/provision"
)

// alertServer is a fake Cloud Monitoring: it records what was sent and
// answers listings from what it holds.
type alertServer struct {
	channels []map[string]any
	policies []map[string]any
	deleted  []string
	last     map[string]any
}

func (s *alertServer) start(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			s.deleted = append(s.deleted, r.URL.Path)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "notificationChannels"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			body["name"] = "projects/p/notificationChannels/" + strings.Repeat("9", 4)
			s.channels = append(s.channels, body)
			s.last = body
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "alertPolicies"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.policies = append(s.policies, body)
			s.last = body
			_, _ = w.Write([]byte(`{}`))
		case strings.Contains(r.URL.Path, "notificationChannels"):
			_ = json.NewEncoder(w).Encode(map[string]any{"notificationChannels": s.channels})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"alertPolicies": s.policies})
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}
}

func labelsFor(app, env string) map[string]string {
	return map[string]string{"tako-app": app, "tako-env": env}
}

// The destination has to land in the label key Google expects for that
// channel type — a wrong key creates a channel that delivers nowhere.
func TestChannelDestinationUsesTheTypesOwnLabel(t *testing.T) {
	cases := []struct{ kind, target, key string }{
		{"email", "ops@example.com", "email_address"},
		{"google_chat", "spaces/AAA", "space"},
		{"webhook_tokenauth", "https://example.com/hook", "url"},
		{"pubsub", "projects/p/topics/x1-alerts", "topic"},
	}
	for _, c := range cases {
		s := &alertServer{}
		ch := &AlertChannels{C: s.start(t), App: "gallery", Env: "x1", Labels: labelsFor("gallery", "x1")}
		err := ch.Create(context.Background(), provision.Object{
			Kind: provision.KindAlertChannel, Name: "oncall",
			Spec: map[string]any{"channelKind": c.kind, "displayName": "x1-oncall", "target": c.target},
		})
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		labels, _ := s.last["labels"].(map[string]any)
		if labels[c.key] != c.target {
			t.Errorf("%s destination landed as %v, want %s=%q", c.kind, labels, c.key, c.target)
		}
	}
}

// Ownership rides userLabels; a policy belonging to someone else in the
// same project must never be observed, updated or deleted.
func TestForeignPoliciesAreInvisible(t *testing.T) {
	s := &alertServer{policies: []map[string]any{
		{"name": "projects/p/alertPolicies/1", "displayName": "x1-Stuck", "userLabels": map[string]any{"tako-app": "gallery", "tako-env": "x1", "tako-spec-hash": "abc"}},
		{"name": "projects/p/alertPolicies/2", "displayName": "Stuck", "userLabels": map[string]any{"tako-app": "gallery", "tako-env": "prod"}},
		{"name": "projects/p/alertPolicies/3", "displayName": "someone else's policy"},
	}}
	pol := &AlertPolicies{C: s.start(t), App: "gallery", Env: "x1", Labels: labelsFor("gallery", "x1")}
	obs, err := pol.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 1 || obs[0].Name != "Stuck" || obs[0].SpecHash != "abc" {
		t.Fatalf("observed = %+v — only this app AND env may be seen", obs)
	}
}

// A policy that names a channel which does not exist would page nobody.
// Creating it silently is worse than refusing.
func TestPolicyRefusesADanglingChannel(t *testing.T) {
	s := &alertServer{}
	c := s.start(t)
	ch := &AlertChannels{C: c, App: "gallery", Env: "x1", Labels: labelsFor("gallery", "x1")}
	pol := &AlertPolicies{C: c, App: "gallery", Env: "x1", Labels: labelsFor("gallery", "x1"), Channels: ch}
	err := pol.Create(context.Background(), provision.Object{
		Kind: provision.KindAlertPolicy, Name: "Stuck",
		Spec: map[string]any{"displayName": "x1-Stuck", "query": "up > 0", "channels": "oncall"},
	})
	if err == nil {
		t.Fatal("a policy naming a missing channel was created — it would alert nobody")
	}
	if !strings.Contains(err.Error(), "oncall") {
		t.Errorf("error does not name the missing channel: %v", err)
	}
}

// The policy must carry the PromQL condition, its channels, and the
// ownership labels the next reconcile reads.
func TestPolicyCarriesQueryChannelsAndLabels(t *testing.T) {
	s := &alertServer{}
	c := s.start(t)
	ch := &AlertChannels{C: c, App: "gallery", Env: "x1", Labels: labelsFor("gallery", "x1")}
	if err := ch.Create(context.Background(), provision.Object{
		Kind: provision.KindAlertChannel, Name: "oncall",
		Spec: map[string]any{"channelKind": "email", "displayName": "x1-oncall", "target": "ops@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	pol := &AlertPolicies{C: c, App: "gallery", Env: "x1", Labels: labelsFor("gallery", "x1"), Channels: ch}
	if err := pol.Create(context.Background(), provision.Object{
		Kind: provision.KindAlertPolicy, Name: "Missed",
		Spec: map[string]any{
			"displayName": "x1-Missed", "query": `absent_over_time(x{a="b"}[172800s])`,
			"evaluationInterval": "300s", "channels": "oncall",
		},
	}); err != nil {
		t.Fatal(err)
	}
	body := s.last
	conds, _ := body["conditions"].([]any)
	if len(conds) != 1 {
		t.Fatalf("conditions = %v", body["conditions"])
	}
	cond := conds[0].(map[string]any)
	pq, ok := cond["conditionPrometheusQueryLanguage"].(map[string]any)
	if !ok {
		t.Fatalf("condition is not PromQL: %v", cond)
	}
	if !strings.HasPrefix(pq["query"].(string), "absent_over_time(") {
		t.Errorf("query = %v", pq["query"])
	}
	// A 48h lookback forbids a retest window, so no duration was set.
	if d, present := pq["duration"]; present && d != "" {
		t.Errorf("duration %v sent with a long lookback — the API refuses it", d)
	}
	if chans, _ := body["notificationChannels"].([]any); len(chans) != 1 {
		t.Errorf("notificationChannels = %v", body["notificationChannels"])
	}
	ul, _ := body["userLabels"].(map[string]any)
	for _, k := range []string{"tako-app", "tako-env", "tako-spec-hash"} {
		if ul[k] == nil || ul[k] == "" {
			t.Errorf("userLabels missing %s: %v", k, ul)
		}
	}
	// The NAME must never ride a label: GCP label values forbid
	// uppercase, and a tako identity is an exported Go var. The live
	// gate refused `tako-name: "Stuck"` outright.
	for k, v := range ul {
		if s, ok := v.(string); ok && s != strings.ToLower(s) {
			t.Errorf("userLabel %s=%q has uppercase — the API refuses it", k, s)
		}
	}
	if body["displayName"] != "x1-Missed" {
		t.Errorf("displayName = %v; the declared name rides here, not a label", body["displayName"])
	}
}

// A disabled Monitoring API means no alerting exists, which observation
// must read as "nothing there" — destroy must never demand an API the
// app never turned on. Same rule the T3 gate established for Tasks.
func TestDisabledMonitoringObservesAsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"Monitoring API has not been used in project p before or it is disabled","status":"PERMISSION_DENIED","details":[{"reason":"SERVICE_DISABLED"}]}}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}
	pol := &AlertPolicies{C: c, App: "gallery", Env: "x1"}
	obs, err := pol.List(context.Background())
	if err != nil || obs != nil {
		t.Fatalf("disabled API must observe as empty, got %v / %v", obs, err)
	}
}
