package gcp

import (
	"strings"
	"testing"
)

// The constructor a metric needs is decided by its SHAPE, and getting
// that wrong costs a failed deploy. A distribution named bare produces a
// policy Cloud Monitoring refuses — which is exactly the mistake the
// search exists to prevent, so it must not make it in its own output.
func TestConstructorFollowsTheMetricShape(t *testing.T) {
	cases := []struct {
		name string
		d    Descriptor
		want string
	}{
		{"gauge on a service", Descriptor{
			Type: "run.googleapis.com/container/instance_count", ValueType: "INT64",
			Resources: []string{"cloud_run_revision"}}, "tako.ServiceMetric(svc,"},
		{"distribution on a service", Descriptor{
			Type: "run.googleapis.com/container/cpu/utilizations", ValueType: "DISTRIBUTION",
			Resources: []string{"cloud_run_revision"}}, "tako.ServicePercentile(svc,"},
		{"counter on a subscription", Descriptor{
			Type: "pubsub.googleapis.com/subscription/dead_letter_message_count", ValueType: "INT64",
			Resources: []string{"pubsub_subscription"}}, "tako.QueueMetric(q,"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.Constructor(); !strings.Contains(got, tc.want) {
				t.Errorf("Constructor() = %q, want it to suggest %q", got, tc.want)
			}
		})
	}
}

// A distribution must never be suggested under its bare name, whatever
// it is scoped to — that name does not exist in PromQL at all.
func TestADistributionIsNeverSuggestedBare(t *testing.T) {
	for _, res := range [][]string{{"cloud_run_revision"}, {"pubsub_subscription"}, nil} {
		d := Descriptor{Type: "x.googleapis.com/thing/latencies", ValueType: "DISTRIBUTION", Resources: res}
		got := d.Constructor()
		if strings.Contains(got, "ServiceMetric(") || strings.Contains(got, "QueueMetric(") {
			t.Errorf("resources %v: suggested a bare-name constructor for a distribution: %q", res, got)
		}
	}
}

// Google's descriptions run to paragraphs with sampling notes. The
// search prints one line per metric, so the description has to be the
// part that identifies it.
func TestDescriptionIsTrimmedToWhatIdentifiesIt(t *testing.T) {
	long := "Distribution of time spent starting a new container instance in milliseconds. " +
		"Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds."
	got := FirstSentence(long)
	if !strings.HasPrefix(got, "Distribution of time spent") {
		t.Errorf("lost the identifying part: %q", got)
	}
	if strings.Contains(got, "Sampled every") {
		t.Errorf("kept the sampling note, which is the same on every metric: %q", got)
	}
}
