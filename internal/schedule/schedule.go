// Package schedule parses tako.Cron schedules: "every <Go duration>"
// or a 5-field unix-cron expression ("0 3 * * *"). The analyzer, the
// runtime, and the provisioner share this parser, so a schedule that
// analyzes always runs locally AND maps onto Cloud Scheduler.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Sched is one parsed schedule: exactly one of Every (an "every"
// interval) or Expr (a validated unix-cron expression) is set.
type Sched struct {
	Every time.Duration
	Expr  string
}

// Parse accepts "every <duration>" or a 5-field unix-cron expression.
func Parse(s string) (Sched, error) {
	if rest, ok := strings.CutPrefix(s, "every "); ok {
		if d, err := time.ParseDuration(rest); err == nil && d > 0 {
			if _, err := everyToCron(d); err != nil {
				return Sched{}, fmt.Errorf("%v", err)
			}
			return Sched{Every: d}, nil
		}
		return Sched{}, fmt.Errorf(`tako.Cron schedule must be "every <duration>" or a 5-field cron expression; got %q`, s)
	}
	if fields := strings.Fields(s); len(fields) == 5 {
		for i, f := range fields {
			if err := checkCronField(f, cronBounds[i]); err != nil {
				return Sched{}, fmt.Errorf("tako.Cron schedule %q: field %d (%s): %v", s, i+1, cronFieldNames[i], err)
			}
		}
		return Sched{Expr: strings.Join(fields, " ")}, nil
	}
	return Sched{}, fmt.Errorf(`tako.Cron schedule must be "every <duration>" or a 5-field cron expression ("0 3 * * *"); got %q`, s)
}

// UnixCron renders the schedule as the unix-cron string Cloud
// Scheduler takes. Parse already guaranteed it exists.
func (s Sched) UnixCron() string {
	if s.Expr != "" {
		return s.Expr
	}
	c, _ := everyToCron(s.Every)
	return c
}

// everyToCron maps an interval onto unix-cron. Only intervals the
// grammar can express map: whole minutes dividing 60, whole hours
// dividing 24, or exactly 24h — anything else must be written as a
// cron expression instead.
func everyToCron(d time.Duration) (string, error) {
	if d == 24*time.Hour {
		return "0 0 * * *", nil
	}
	if d >= time.Hour && d%time.Hour == 0 {
		h := int(d / time.Hour)
		if h < 24 && 24%h == 0 {
			return fmt.Sprintf("0 */%d * * *", h), nil
		}
	}
	if d >= time.Minute && d%time.Minute == 0 && d < time.Hour {
		m := int(d / time.Minute)
		if 60%m == 0 {
			if m == 1 {
				return "* * * * *", nil
			}
			return fmt.Sprintf("*/%d * * * *", m), nil
		}
	}
	return "", fmt.Errorf(`"every %s" cannot map onto Cloud Scheduler's unix-cron grammar — use minutes dividing 60, hours dividing 24, exactly 24h, or write a cron expression`, d)
}

// --- 5-field cron: validation and next-fire ---------------------------

var cronBounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
var cronFieldNames = [5]string{"minute", "hour", "day-of-month", "month", "day-of-week"}

// checkCronField validates one field: *, */N, N, N-M, and comma lists
// of those.
func checkCronField(f string, bounds [2]int) error {
	for _, part := range strings.Split(f, ",") {
		if part == "*" {
			continue
		}
		if step, ok := strings.CutPrefix(part, "*/"); ok {
			n, err := strconv.Atoi(step)
			if err != nil || n <= 0 || n > bounds[1] {
				return fmt.Errorf("bad step %q", part)
			}
			continue
		}
		lo, hi, found := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < bounds[0] || a > bounds[1] {
			return fmt.Errorf("value %q out of range %d–%d", part, bounds[0], bounds[1])
		}
		if found {
			b, err := strconv.Atoi(hi)
			if err != nil || b < a || b > bounds[1] {
				return fmt.Errorf("bad range %q", part)
			}
		}
	}
	return nil
}

// fieldMatches reports whether v satisfies one validated field.
func fieldMatches(f string, v int) bool {
	for _, part := range strings.Split(f, ",") {
		if part == "*" {
			return true
		}
		if step, ok := strings.CutPrefix(part, "*/"); ok {
			n, _ := strconv.Atoi(step)
			if n > 0 && v%n == 0 {
				return true
			}
			continue
		}
		lo, hi, found := strings.Cut(part, "-")
		a, _ := strconv.Atoi(lo)
		if !found {
			if v == a {
				return true
			}
			continue
		}
		b, _ := strconv.Atoi(hi)
		if v >= a && v <= b {
			return true
		}
	}
	return false
}

// Next returns the first time strictly after t that the schedule
// matches, in t's location. Standard cron semantics: when BOTH
// day-of-month and day-of-week are restricted, either matching fires.
func (s Sched) Next(t time.Time) time.Time {
	if s.Expr == "" {
		return t.Add(s.Every)
	}
	f := strings.Fields(s.Expr)
	tick := t.Truncate(time.Minute).Add(time.Minute)
	limit := tick.AddDate(5, 0, 0)
	for ; tick.Before(limit); tick = tick.Add(time.Minute) {
		if !fieldMatches(f[0], tick.Minute()) || !fieldMatches(f[1], tick.Hour()) || !fieldMatches(f[3], int(tick.Month())) {
			continue
		}
		domStar, dowStar := f[2] == "*", f[4] == "*"
		dom := fieldMatches(f[2], tick.Day())
		dow := fieldMatches(f[4], int(tick.Weekday()))
		switch {
		case domStar && dowStar, domStar && dow, dowStar && dom:
		case !domStar && !dowStar && (dom || dow):
		default:
			continue
		}
		return tick
	}
	return limit
}
