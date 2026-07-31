package schedule

import (
	"strings"
	"testing"
	"time"
)

func TestParseEvery(t *testing.T) {
	for _, tc := range []struct {
		in   string
		cron string
	}{
		{"every 1m", "* * * * *"},
		{"every 10m", "*/10 * * * *"},
		{"every 6h", "0 */6 * * *"},
		{"every 24h", "0 0 * * *"},
	} {
		s, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.in, err)
		}
		if got := s.UnixCron(); got != tc.cron {
			t.Fatalf("UnixCron(%q) = %q, want %q", tc.in, got, tc.cron)
		}
	}
}

func TestParseEveryUnmappable(t *testing.T) {
	for _, in := range []string{"every 7m", "every 90s", "every 5h", "every 48h"} {
		if _, err := Parse(in); err == nil || !strings.Contains(err.Error(), "unix-cron grammar") {
			t.Fatalf("Parse(%q) should refuse with the grammar pointer; got %v", in, err)
		}
	}
}

func TestParseExpr(t *testing.T) {
	for _, in := range []string{"0 3 * * *", "*/5 * * * *", "0 9-17 * * 1-5", "30 6 1,15 * *"} {
		s, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if s.UnixCron() != in {
			t.Fatalf("expr should round-trip: %q → %q", in, s.UnixCron())
		}
	}
	for _, in := range []string{"61 * * * *", "* 25 * * *", "* * * * * *", "0 3 * *", "a b c d e"} {
		if _, err := Parse(in); err == nil {
			t.Fatalf("Parse(%q) should fail", in)
		}
	}
}

func TestNext(t *testing.T) {
	base := time.Date(2026, 7, 31, 2, 30, 45, 0, time.UTC)
	for _, tc := range []struct {
		expr string
		want time.Time
	}{
		{"0 3 * * *", time.Date(2026, 7, 31, 3, 0, 0, 0, time.UTC)},
		{"*/5 * * * *", time.Date(2026, 7, 31, 2, 35, 0, 0, time.UTC)},
		// 2026-07-31 is a Friday; next Monday 09:00 is Aug 3.
		{"0 9 * * 1", time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)},
		{"30 6 1 * *", time.Date(2026, 8, 1, 6, 30, 0, 0, time.UTC)},
	} {
		s, err := Parse(tc.expr)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Next(base); !got.Equal(tc.want) {
			t.Fatalf("Next(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
	s, _ := Parse("every 10m")
	if got := s.Next(base); !got.Equal(base.Add(10*time.Minute)) {
		t.Fatalf("every Next = %v", got)
	}
}
