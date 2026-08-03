package provision

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// The whole path: graph → desired objects → plan. A channel and a
// policy must appear, in that order, with the policy's query built.
func TestAlertsReachTheDesiredPlan(t *testing.T) {
	cfg := Config{Project: "p", ProjectNumber: "12345", Region: "us-central1", App: "gallery", Env: "x1"}
	g := &analyzer.AppGraph{
		Module:   "gallery",
		Services: []analyzer.Service{{Name: "gallery", Package: "gallery"}},
		Handlers: []analyzer.Handler{
			{Kind: "cron", Var: "gallery.Cleanup", Service: "gallery", Schedule: "every 24h"},
		},
		Resources: []analyzer.Resource{
			{Kind: "topic", Name: "incidents", Var: "gallery.incidents", Package: "gallery"},
			{Kind: "channel", Name: "oncall", ChannelKind: "email", Target: "ops@example.com", Var: "gallery.oncall"},
			{Kind: "channel", Name: "bus", ChannelKind: "pubsub", Target: "incidents", Var: "gallery.bus"},
			{Kind: "alert", Name: "Missed", Var: "gallery.Missed", Channels: []string{"gallery.oncall", "gallery.bus"},
				Condition: "CronMissed",
				Alert: &analyzer.AlertCondition{
					Op: "absent", Reading: "cron_missed", SubjectKind: "cron", SubjectVar: "gallery.Cleanup"}},
		},
	}
	objs, err := alertObjects(cfg, g)
	if err != nil {
		t.Fatal(err)
	}
	var chans, pols []Object
	for _, o := range objs {
		switch o.Kind {
		case KindAlertChannel:
			chans = append(chans, o)
		case KindAlertPolicy:
			pols = append(pols, o)
		}
	}
	if len(chans) != 2 || len(pols) != 1 {
		t.Fatalf("got %d channels, %d policies", len(chans), len(pols))
	}
	// A pubsub channel needs the FULL topic path, env-prefixed — the
	// same name the topic reconciler created.
	var bus Object
	for _, c := range chans {
		if c.Name == "bus" {
			bus = c
		}
	}
	if got := bus.Spec["target"]; got != "projects/p/topics/x1-incidents" {
		t.Errorf("pubsub channel target = %v, want the full env-prefixed topic path", got)
	}
	q := pols[0].Spec["query"].(string)
	if !strings.Contains(q, "absent_over_time(") || !strings.Contains(q, "[172800s]") {
		t.Errorf("query = %q, want a 48h absence (twice the daily schedule)", q)
	}
	// A 48h lookback forbids a retest window, so no duration is planned.
	if _, present := pols[0].Spec["duration"]; present {
		t.Errorf("duration planned with a 48h lookback — the API refuses it: %v", pols[0].Spec)
	}
	if got := pols[0].Spec["channels"]; got != "oncall,bus" {
		t.Errorf("channels = %v, want the short var names both adapters key on", got)
	}
}

// The Monitoring notification agent must be granted publisher on a
// pubsub channel's topic, or the channel is created and silently never
// delivers.
func TestPubSubChannelGrantsTheMonitoringAgent(t *testing.T) {
	cfg := Config{Project: "p", ProjectNumber: "12345", Region: "us-central1", App: "gallery", Env: "x1"}
	g := &analyzer.AppGraph{
		Services:  []analyzer.Service{{Name: "gallery", Package: "gallery"}},
		Resources: []analyzer.Resource{{Kind: "channel", Name: "bus", ChannelKind: "pubsub", Target: "incidents"}},
	}
	var found bool
	for _, o := range deriveGrants(g, cfg) {
		if !strings.Contains(o.Name, "x1-incidents") {
			continue
		}
		if strings.Contains(fmt.Sprint(o.Spec), "gcp-sa-monitoring-notification") &&
			strings.Contains(fmt.Sprint(o.Spec), "roles/pubsub.publisher") {
			found = true
		}
	}
	if !found {
		t.Fatal("no pubsub.publisher grant for the Monitoring notification agent — the channel would deliver nothing")
	}
}
