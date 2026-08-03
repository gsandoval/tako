package main

import (
	"os"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/provision"
)

// Deploy applies in hand-written phases — one plan.Filter per kind or
// group — so a kind can be added to reconcileOrder, routed to an
// applier, planned, printed, and then never applied. That is exactly
// what happened to the two alerting kinds: the plan said
// "+ create alert policy Silent", the deploy exited 0, and nothing
// existed in the project.
//
// Nothing failed, because nothing was checked. This scans deploy's
// source and fails when a kind in reconcileOrder is never filtered for.
func TestEveryReconciledKindIsApplied(t *testing.T) {
	src, err := os.ReadFile("deploy.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)

	// The kind constants, by the identifier deploy names them with.
	names := map[provision.Kind]string{
		provision.KindRepo:               "KindRepo",
		provision.KindServiceAccount:     "KindServiceAccount",
		provision.KindBucket:             "KindBucket",
		provision.KindDatastoreIndex:     "KindDatastoreIndex",
		provision.KindTopic:              "KindTopic",
		provision.KindBucketNotification: "KindBucketNotification",
		provision.KindPullSubscription:   "KindPullSubscription",
		provision.KindTasksQueue:         "KindTasksQueue",
		provision.KindRunService:         "KindRunService",
		provision.KindRunJob:             "KindRunJob",
		provision.KindIamGrants:          "KindIamGrants",
		provision.KindPushSubscription:   "KindPushSubscription",
		provision.KindSchedulerJob:       "KindSchedulerJob",
		provision.KindDomainMapping:      "KindDomainMapping",
		provision.KindAlertChannel:       "KindAlertChannel",
		provision.KindAlertPolicy:        "KindAlertPolicy",
	}
	for _, kind := range provision.DeleteOrder() { // every kind, order irrelevant
		ident, known := names[kind]
		if !known {
			t.Errorf("kind %q is reconciled but this test does not know its identifier — add it, then check deploy applies it", kind)
			continue
		}
		if !strings.Contains(text, "provision."+ident) {
			t.Errorf("kind %q (provision.%s) is in reconcileOrder but deploy never applies it — "+
				"it will be planned, printed, and silently skipped", kind, ident)
		}
	}
}
