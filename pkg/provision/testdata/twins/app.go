// Package twins declares each preset beside the hand-written condition
// that is supposed to replace it.
//
// This is the proof of FA2 and FA3's central claim: that a preset is a
// starting point rather than a ceiling — read what it compiles to with
// `tako alerts`, then write your own version. That claim is only worth
// making if the two really do compile to the same PromQL, character for
// character. TestPresetsAndTheirHandWrittenTwinsAgree is what holds it
// to that.
//
// It lives in testdata rather than in examples/siren because these are
// duplicates by construction. Deploying every preset twice would double
// a live walk's policies and page twice for one event; what is being
// tested is compilation, not behaviour.
package twins

import (
	"time"

	"github.com/gsandoval/tako"
)

var (
	oncall = tako.AlertEmail("ops@example.com")

	work  = tako.Queue[string]("work")
	Grind = tako.Worker(work, grind)
	Boom  = tako.API("GET /boom", boom)
	Sweep = tako.Cron("every 10m", sweep)
)

func grind(ctx tako.Ctx, s string) error            { return nil }
func sweep(ctx tako.Ctx) error                      { return nil }
func boom(ctx tako.Ctx, _ struct{}) (string, error) { return "", nil }

// Each pair is <Name>Preset and <Name>ByHand. The test finds them by
// that suffix convention, so a pair added here is checked without the
// test being edited — and a preset with no twin is REPORTED, which is
// what keeps this from silently covering less over time.
var (
	// A ratio of 5xx to all requests on one endpoint.
	ErroringPreset = tako.Alert(
		tako.RequestsErroring(Boom, 0.05, tako.Window{For: 5 * time.Minute}), oncall)
	ErroringByHand = tako.Alert(
		tako.WhenAbove(tako.Requests(Boom).Status("5xx").Fraction(), 0.05,
			tako.Window{For: 5 * time.Minute}), oncall)

	// A quantile over the latency distribution for one endpoint.
	SlowPreset = tako.Alert(
		tako.RequestsSlow(Boom, 2*time.Second, 0.99, tako.Window{For: 5 * time.Minute}), oncall)
	SlowByHand = tako.Alert(
		tako.WhenAbove(tako.RequestMs(Boom).Percentile(0.99), 2000,
			tako.Window{For: 5 * time.Minute}), oncall)

	// A cron that has not run. The lookback is twice the schedule, which
	// the preset derives from the graph — so the twin has to say it.
	MissedPreset = tako.Alert(
		tako.CronMissed(Sweep), oncall)
	MissedByHand = tako.Alert(
		// Every is stated only because the Count rule asks for it; an
		// absent condition never uses a counting period. Logged as
		// discovered work rather than fixed here.
		tako.WhenAbsent(tako.Requests(Sweep).Count(),
			tako.Window{For: 20 * time.Minute, Every: time.Minute}), oncall)

	// A count of a cron's failures.
	CronFailPreset = tako.Alert(
		tako.CronFailed(Sweep, 1, tako.Window{Every: 5 * time.Minute}), oncall)
	CronFailByHand = tako.Alert(
		tako.WhenAtLeast(tako.Requests(Sweep).Status("5xx").Count(), 1,
			tako.Window{Every: 5 * time.Minute}), oncall)

	// The instance-count conditions, which were already expressible —
	// pinned here so that stays true.
	CapacityPreset = tako.Alert(
		tako.ServiceAtCapacity(Grind, tako.Window{For: 5 * time.Minute}), oncall)
	CapacityByHand = tako.Alert(
		tako.WhenAbove(tako.ServiceInstances(Grind), 99.5,
			tako.Window{For: 5 * time.Minute}), oncall)

	// A backlog on the live subscription.
	BackloggedPreset = tako.Alert(
		tako.QueueBacklogged(work, 50, tako.QueueWatch{For: 5 * time.Minute}), oncall)
	BackloggedByHand = tako.Alert(
		tako.WhenAtLeast(tako.QueueMetric(work,
			"pubsub.googleapis.com/subscription/num_undelivered_messages"), 50,
			tako.Window{For: 5 * time.Minute}), oncall)
)
