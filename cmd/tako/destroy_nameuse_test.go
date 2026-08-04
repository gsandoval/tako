package main

import (
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/provision"
)

// A destroy is otherwise fully reversible — every object it deletes comes
// back on the next deploy. A Cloud Tasks queue does not: Google blocks
// the name for 7 days. The error that says so arrives on the redeploy,
// with the clock already running and nothing left to decide. The preview
// is ahead of the confirmation prompt, so it is the last moment where
// knowing this can change anything.
func TestDestroyWarnsAboutTasksQueueNameReuse(t *testing.T) {
	observed := []provision.Observed{
		{Kind: provision.KindRunService, Name: "u1-mailer"},
		{Kind: provision.KindTasksQueue, Name: "u1-mailer-tako"},
		{Kind: provision.KindTasksQueue, Name: "u1-digest-tako"},
	}
	got := nameReuseWarning(observed)
	for _, want := range []string{
		"NAME REUSE", "7 days", "u1-mailer-tako", "u1-digest-tako", "--env",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("warning:\n%s\n  missing %q", got, want)
		}
	}
	if strings.Contains(got, "u1-mailer\n") {
		t.Errorf("warning names a service, not just queues:\n%s", got)
	}
}

// An app with no rate-limited queues must not read a warning about
// queues. Every line in the destroy preview is a consequence of THIS
// teardown; a standing caveat would train people to skip the block.
func TestDestroyIsSilentWithNoTasksQueues(t *testing.T) {
	observed := []provision.Observed{
		{Kind: provision.KindRunService, Name: "u1-mailer"},
		{Kind: provision.KindTopic, Name: "u1-work"},
	}
	if got := nameReuseWarning(observed); got != "" {
		t.Errorf("warned with no tasks queue observed:\n%s", got)
	}
	if got := nameReuseWarning(nil); got != "" {
		t.Errorf("warned on an empty inventory:\n%s", got)
	}
}

// The CLI's number and the provisioner's must be the same number. Two
// copies of "7 days" is how a doc and a behaviour come apart.
func TestWarningAndErrorAgreeOnTheWait(t *testing.T) {
	got := nameReuseWarning([]provision.Observed{{Kind: provision.KindTasksQueue, Name: "q-tako"}})
	if !strings.Contains(got, "7 days") {
		t.Errorf("warning does not carry the shared constant: %s", got)
	}
}
