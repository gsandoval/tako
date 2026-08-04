package provision

// What an alert IS, rendered for a human.
//
// This exists so `tako alerts` and the siren golden are one renderer
// rather than two. The golden is how this repo catches a query that
// matches nothing; the command is how a user catches the same thing in
// their own app. They should never be able to disagree about what a
// condition compiles to — that disagreement is the whole class of defect
// FA1 closed one layer down.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// AlertDescription is one declared alert, resolved.
type AlertDescription struct {
	// Name is the declaring var — the policy's display name.
	Name string
	// Sentence is what the notification will say.
	Sentence string
	// Query is the PromQL Cloud Monitoring will evaluate. This is the
	// artifact: everything else describes it.
	Query string
	// Ctor is the SDK call that produced it, e.g. "RequestsSlow". Empty
	// only for a condition the analyzer could not attribute.
	Ctor string
	// Channels are the declared destinations, by var name.
	Channels []string
	// For and Every are the window, already defaulted the way the deploy
	// will send them — a documented default that the deploy does not
	// actually apply is a lie the user cannot see.
	For   time.Duration
	Every time.Duration
	// Retest reports whether Cloud Monitoring will accept a retest
	// window for this lookback. It is false for long absences, and the
	// reason is worth showing rather than hiding.
	Retest bool
}

// DescribeAlerts resolves every alert the app declares.
//
// Pure: it reads the graph and the config and calls nothing. That is
// what lets `tako alerts` work with no project, no credentials and no
// network — an alert is a property of the code, and only the env-prefixed
// names in it depend on where it would be deployed.
func DescribeAlerts(cfg Config, g *analyzer.AppGraph) ([]AlertDescription, error) {
	var out []AlertDescription
	for _, r := range g.Resources {
		if r.Kind != "alert" {
			continue
		}
		q, retest, err := alertQuery(cfg, g, r.Alert)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.Name, err)
		}
		d := AlertDescription{
			Name:     r.Name,
			Sentence: conditionSentence(cfg, g, r),
			Query:    q,
			Channels: append([]string(nil), shortNames(r.Channels)...),
			Retest:   retest,
		}
		if r.Alert != nil {
			d.Ctor = r.Alert.Ctor
			d.For = time.Duration(r.Alert.ForSeconds) * time.Second
			d.Every = evaluationInterval(r.Alert)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Render prints one alert the way `tako alerts` shows it.
//
// The query is printed WHOLE and unwrapped. It is meant to be copied
// into the metrics explorer and run, and a line-wrapped PromQL
// expression is one someone has to repair first.
func (d AlertDescription) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s\n", d.Name, d.Sentence)
	fmt.Fprintf(&b, "  when   %s\n", d.window())
	fmt.Fprintf(&b, "  query  %s\n", d.Query)
	if len(d.Channels) > 0 {
		fmt.Fprintf(&b, "  notify %s\n", strings.Join(d.Channels, ", "))
	}
	if d.Ctor != "" {
		fmt.Fprintf(&b, "  from   tako.%s\n", d.Ctor)
	}
	return b.String()
}

func (d AlertDescription) window() string {
	parts := []string{fmt.Sprintf("evaluated every %s", d.Every)}
	if d.For > 0 {
		parts = append(parts, fmt.Sprintf("sustained %s", d.For))
	} else if !strings.HasPrefix(d.Query, "absent_over_time(") {
		// No sustain window means it fires on the first breaching
		// sample, which is a real property and not a missing value.
		parts = append(parts, "fires on the first breach")
	}
	if !d.Retest {
		// Google refuses a retest window past 1d1h, so tako sends
		// duration 0. Saying so beats letting someone wonder why their
		// For did not survive the deploy.
		parts = append(parts, "no retest window (lookback over 1d1h — Cloud Monitoring refuses one)")
	}
	return strings.Join(parts, ", ")
}
