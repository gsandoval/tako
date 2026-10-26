package main

import (
	"slices"
	"strings"
	"testing"
)

func TestNormalizeArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
		{
			name: "splits long flag equals value",
			in:   []string{"--limit=10", "--since=5m", "--push=us-central1-docker.pkg.dev/p/r", "--kind=site"},
			want: []string{"--limit", "10", "--since", "5m", "--push", "us-central1-docker.pkg.dev/p/r", "--kind", "site"},
		},
		{
			name: "preserves value containing equals",
			in:   []string{"--member=user:a=b@example.com"},
			want: []string{"--member", "user:a=b@example.com"},
		},
		{
			name: "stops after bare double dash",
			in:   []string{"--env=staging", "--", "--raw=1", "positional"},
			want: []string{"--env", "staging", "--", "--raw=1", "positional"},
		},
		{
			name: "leaves single dash and positional args untouched",
			in:   []string{"-v", "-run=TestFoo", "key=value"},
			want: []string{"-v", "-run=TestFoo", "key=value"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeArgs(tc.in)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("normalizeArgs(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSubcommandsAcceptEqualsFlags(t *testing.T) {
	// runMetrics: --limit=0 should be parsed as the --limit value and
	// trigger the positive-number validation rather than "unknown flag"
	// or treating "--limit=0" as the search term.
	err := runMetrics([]string{"cpu", "--project=my-proj", "--limit=0"})
	if err == nil || !strings.Contains(err.Error(), "--limit wants a positive number") {
		t.Fatalf("runMetrics(--limit=0) = %v, want --limit validation error", err)
	}

	// runLogs: --since=bad should be parsed as the --since value and
	// trigger duration validation rather than being treated as a service name.
	err = runLogs([]string{"--project=my-proj", "--since=not-a-duration"})
	if err == nil || !strings.Contains(err.Error(), "--since wants a duration") {
		t.Fatalf("runLogs(--since=not-a-duration) = %v, want --since validation error", err)
	}

	// runNew: --kind=invalid should be parsed as --kind and trigger the
	// kind validation rather than "unknown flag --kind=invalid".
	err = runNew([]string{"myapp", "--kind=invalid"})
	if err == nil || !strings.Contains(err.Error(), "unknown --kind") {
		t.Fatalf("runNew(--kind=invalid) = %v, want --kind validation error", err)
	}
}
