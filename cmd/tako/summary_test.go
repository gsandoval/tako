package main

import (
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// The security summary used to recite constants — "retries: 3",
// "redelivery: 5 attempts" — that were true only until V2 and V3 made
// them declarable. A summary stating a number the deploy does not use is
// worse than one that says nothing.
func TestSummaryReadsDeclaredCountsNotDefaults(t *testing.T) {
	graph := &analyzer.AppGraph{
		Resources: []analyzer.Resource{
			{Kind: "queue", Name: "thumbnails", MaxAttempts: 8},
			{Kind: "queue", Name: "plain"},
		},
		Handlers: []analyzer.Handler{
			{Kind: "worker", Var: "g.MakeThumb", Service: "g-makethumb", Binding: "thumbnails"},
			{Kind: "worker", Var: "g.Plain", Service: "g-plain", Binding: "plain"},
			{Kind: "job", Var: "g.Settle", Service: "g-settle", Retries: 1},
			{Kind: "job", Var: "g.Other", Service: "g-other"},
		},
	}
	if got := deliveryAttemptsOf(graph, "g-makethumb"); got != 8 {
		t.Errorf("declared attempts = %d, want 8", got)
	}
	if got := deliveryAttemptsOf(graph, "g-plain"); got != analyzer.DeliveryDefaults.MaxAttempts {
		t.Errorf("undeclared attempts = %d, want the framework default %d", got, analyzer.DeliveryDefaults.MaxAttempts)
	}
	if got := jobRetriesOf(graph, "g-settle"); got != 1 {
		t.Errorf("declared retries = %d, want 1", got)
	}
	if got := jobRetriesOf(graph, "g-other"); got != 3 {
		t.Errorf("undeclared retries = %d, want Cloud Run's 3", got)
	}
}

// Same trap, one slice later: the project-scope line explained itself
// with Datastore's reason, and U2 put a second role on the project. A
// metrics grant described as a Datastore quirk understates what it is.
func TestProjectGrantsExplainTheirOwnScope(t *testing.T) {
	metrics := projectGrantReason("roles/monitoring.metricWriter")
	datastore := projectGrantReason("roles/datastore.user")
	if metrics == datastore {
		t.Error("a metrics grant and a datastore grant are project-wide for different reasons; one explanation cannot serve both")
	}
	if !strings.Contains(metrics, "every service") {
		t.Errorf("the metrics reason must say the grant is package-wide, got %q", metrics)
	}
	if !strings.Contains(datastore, "Datastore") {
		t.Errorf("the datastore reason must still name Datastore, got %q", datastore)
	}
}
