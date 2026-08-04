package alerts

import (
	"time"

	"github.com/gsandoval/tako"
)

var (
	thumbs = tako.Queue[string]("thumbs")
	lat    = tako.Histogram("thumb_ms")

	oncall = tako.AlertEmail("ops@example.com")
	// Not a constant: destinations are read at analysis time.
	loose = tako.AlertEmail(address())

	Work    = tako.Worker(thumbs, work)
	Cleanup = tako.Cron("every 24h", tick)
)

func address() string                   { return "x@y.z" }
func work(ctx tako.Ctx, s string) error { return nil }
func tick(ctx tako.Ctx) error           { return nil }

var (
	// CronMissed watches a cron; Work is a worker. Every async handler
	// is a tako.Handler, so only the analyzer can catch this.
	WrongKind = tako.Alert(tako.CronMissed(Work), oncall)

	// A quantile is a fraction, not a percentage.
	BadQuantile = tako.Alert(tako.RequestsSlow(Photos, time.Second, 99), oncall)
	BadPct      = tako.Alert(tako.WhenAbove(lat.Percentile(99), 10, tako.Window{For: time.Minute}), oncall)

	// A destination that is not a declared channel var.
	Undeclared = tako.Alert(tako.CronMissed(Cleanup), tako.AlertEmail("adhoc@example.com"))
)

var Photos = tako.API("GET /photos", list)

func list(ctx tako.Ctx, _ struct{}) (string, error) { return "", nil }

var _ = loose

// Windows: the ceilings are Cloud Monitoring's, measured, and refused
// here rather than at deploy time as a 400.
var (
	TooOften = tako.Alert(tako.WhenAbove(hits.Rate(), 1, tako.Window{For: time.Minute, Every: 30 * time.Second}), oncall)
	TooLong  = tako.Alert(tako.WhenAbove(hits.Rate(), 1, tako.Window{For: 48 * time.Hour}), oncall)
	NoPeriod = tako.Alert(tako.WhenBelow(hits.Count(), 1, tako.Window{For: time.Hour}), oncall)
)

var hits = tako.Counter("hits")

// A queue condition with no sustain window: a queue crosses any depth
// for an instant, so For is required rather than defaulted.
var NoSustain = tako.Alert(tako.QueueBacklogged(thumbs, 1, tako.QueueWatch{DeadLetters: true}), oncall)

// A matcher on a label tako already writes. PromQL accepts both and
// matches neither, so the query is well-formed and permanently empty —
// which reads exactly like a healthy queue.
var (
	DoubleSub = tako.Alert(tako.WhenAbove(
		tako.QueueMetric(thumbs, "pubsub.googleapis.com/subscription/num_undelivered_messages",
			`subscription_id="x1-alerts-work-dead"`), 1, tako.Window{For: time.Minute}), oncall)

	DoubleService = tako.Alert(tako.WhenAbove(
		tako.ServiceMetric(Work, "run.googleapis.com/container/memory/utilizations",
			`service_name="alerts"`), 0.9, tako.Window{For: time.Minute}), oncall)

	DoubleResource = tako.Alert(tako.WhenAbove(
		tako.PlatformMetric("pubsub.googleapis.com/topic/byte_cost",
			`monitored_resource="pubsub_topic"`), 1, tako.Window{For: time.Minute}), oncall)
)

// The request readers: a status CLASS, not a code, and a Fraction that
// narrows to something. Both are queries that would deploy clean and
// match nothing.
//
// A reading a handle does not have — RequestMs(x).Fraction() — needs no
// rule here: the handles are distinct types, so the compiler refuses it
// before the analyzer runs. That is the better guard, and it is why
// there is no "no such reading" message to test.
var (
	BadStatus = tako.Alert(tako.WhenAbove(
		tako.Requests(Photos).Status("503").Rate(), 1, tako.Window{For: time.Minute}), oncall)

	WholeFraction = tako.Alert(tako.WhenAbove(
		tako.Requests(Photos).Fraction(), 0.5, tako.Window{For: time.Minute}), oncall)
)
