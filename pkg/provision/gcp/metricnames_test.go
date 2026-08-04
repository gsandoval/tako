package gcp

import (
	"strings"
	"testing"
)

// ServicePercentile on a metric that is NOT a distribution is the one
// new way to be wrong that Z3 adds: the _bucket series will not exist,
// so the query names something unresolvable. The preflight has to say
// which alert and why, rather than letting the apply fail on Google's
// own sentence halfway through.
func TestPreflightExplainsANonDistribution(t *testing.T) {
	known := map[string]bool{
		// A gauge: published bare, with no bucket series.
		"run_googleapis_com:container_instance_count": true,
		// A distribution: published only in parts.
		"run_googleapis_com:container_cpu_utilizations_bucket": true,
		"run_googleapis_com:container_cpu_utilizations_count":  true,
		"run_googleapis_com:container_cpu_utilizations_sum":    true,
	}
	bad := CheckMetricNames(known, map[string]string{
		"Hot":     "run_googleapis_com:container_cpu_utilizations", // named bare — a distribution
		"Crowded": "run_googleapis_com:container_instance_count",   // fine
		"Typo":    "run_googleapis_com:container_instance_countt",  // nothing like it exists
	})
	if len(bad) != 2 {
		t.Fatalf("flagged %d alerts, want 2 (Hot and Typo): %+v", len(bad), bad)
	}
	byName := map[string]UnresolvableMetric{}
	for _, u := range bad {
		byName[u.Alert] = u
	}
	// The distribution gets the parts it IS published as, which is the
	// difference between "wait for data" and "you cannot name it this
	// way at all".
	if got := byName["Hot"].Suffixed; len(got) != 3 {
		t.Errorf("Hot lists %v as available parts, want all three — without them the message cannot say it is a distribution", got)
	}
	// A plain typo has no parts, so the message must not claim it is a
	// distribution.
	if got := byName["Typo"].Suffixed; len(got) != 0 {
		t.Errorf("Typo lists parts %v — it is simply unknown, and saying otherwise sends the reader hunting for a suffix", got)
	}
	msg := UnresolvableError(bad).Error()
	for _, want := range []string{"Hot", "DISTRIBUTION", "_bucket", "Typo", "nothing has written this metric"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message never mentions %q:\n%s", want, msg)
		}
	}
}
