package provision

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/backends"
)

func TestCheckSkewDetectsEntityMessagingRPCAndFlagSkew(t *testing.T) {
	prev := &analyzer.AppGraph{
		Module: "shop",
		Resources: []analyzer.Resource{
			{Kind: "entity", Name: "profiles", Type: "ProfileV1", Indexes: []string{"Email", "Tier"}, TTLField: "expires_at"},
			{Kind: "queue", Name: "orders", Type: "OrderMsgV1"},
			{Kind: "flag", Name: "max-items", Type: "int"},
		},
		Handlers: []analyzer.Handler{
			{Kind: "api", Var: "billing.Charge", Route: "POST /charge", ReqRef: "billing.ChargeReqV1", ResRef: "billing.ChargeRes"},
		},
		Edges: []analyzer.Edge{
			{From: "checkout.Buy", To: "billing.Charge", Kind: "call", Verb: "call"},
		},
	}

	next := &analyzer.AppGraph{
		Module: "shop",
		Resources: []analyzer.Resource{
			{Kind: "entity", Name: "profiles", Type: "ProfileV2", Indexes: []string{"Email"}, TTLField: "ttl"},
			{Kind: "queue", Name: "orders", Type: "OrderMsgV2"},
			{Kind: "flag", Name: "max-items", Type: "int"},
		},
		Handlers: []analyzer.Handler{
			{Kind: "api", Var: "billing.Charge", Route: "POST /charge", ReqRef: "billing.ChargeReqV2", ResRef: "billing.ChargeRes"},
		},
		Edges: []analyzer.Edge{
			{From: "checkout.Buy", To: "billing.Charge", Kind: "call", Verb: "call"},
		},
	}

	activeFlags := []backends.FlagRule{
		{Name: "max-items", Value: json.RawMessage(`"not-an-int"`)},
		{Name: "deleted-flag", Value: json.RawMessage(`true`)},
	}

	rep := CheckSkew(prev, next, activeFlags)
	if !rep.HasDanger() {
		t.Fatalf("expected HasDanger() == true, got findings: %+v", rep.Findings)
	}
	rendered := rep.Render()
	for _, want := range []string{
		"entity struct type changed from ProfileV1 to ProfileV2",
		"indexed field \"Tier\" is no longer queried",
		"queue payload type changed from OrderMsgV1 to OrderMsgV2",
		"cross-service RPC endpoint changed",
		"live flag value \"not-an-int\" does not match binary's declared type int",
		"orphaned rule",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in skew report:\n%s", want, rendered)
		}
	}
}
