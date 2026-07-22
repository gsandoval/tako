// Package schedule parses tako.Cron schedules. v0 accepts exactly
// "every <Go duration>" — cron expressions arrive with cloud scheduling.
// The analyzer and the runtime share this parser so a schedule that
// analyzes always runs.
package schedule

import (
	"fmt"
	"strings"
	"time"
)

// Parse returns the interval of an "every <duration>" schedule.
func Parse(s string) (time.Duration, error) {
	rest, ok := strings.CutPrefix(s, "every ")
	if ok {
		if d, err := time.ParseDuration(rest); err == nil && d > 0 {
			return d, nil
		}
	}
	return 0, fmt.Errorf("tako.Cron schedule must be %q for now (cron expressions arrive with cloud scheduling); got %q", "every <duration>", s)
}
