// Package siren is the example that exists to fail.
//
// Every other example has to WORK for its own walk, which makes it a
// poor place to prove that alerting notices things going wrong: breaking
// gallery's pipeline on purpose fights gallery's own gate. siren has no
// product to protect. Each endpoint is a switch that turns one failure
// mode on, so every condition tako can express has something here that
// makes it fire.
//
// It is also the answer to the standing rule that nothing ships without
// an example demanding it. Alerting exported two dozen symbols against
// five the examples used; the rest either found a demand here or were
// cut.
package siren

import (
	"fmt"
	"time"

	"github.com/gsandoval/tako"
)

// ── what siren is made of ───────────────────────────────────────────

var (
	work   = tako.Queue[Task]("work")
	alerts = tako.Topic[Incident]("incidents")

	hits   = tako.Counter("hits")
	workMs = tako.Histogram("work_ms")
)

// Task is a unit of work whose fate the caller chooses.
type Task struct {
	// Fail is "" (succeed), "soft" (retry until Pub/Sub gives up) or
	// "hard" (tako.Permanent — dead-letter on the first attempt).
	Fail string `json:"fail"`
	// HoldMs makes the worker slow, which is what lets a backlog build.
	HoldMs int `json:"hold_ms"`
	// BurnMs makes the worker slow AND busy. Holding is a sleep and
	// uses no CPU; saturation needs work.
	BurnMs int `json:"burn_ms"`
}

// spin consumes CPU for d, as opposed to waiting for it.
func spin(d time.Duration) float64 {
	deadline := time.Now().Add(d)
	var x float64
	for time.Now().Before(deadline) {
		for i := 0; i < 1e6; i++ {
			x += float64(i) * 1.000001
		}
	}
	return x
}

// Incident is Google's alert payload, narrowed to the fields worth
// routing on. It is deliberately declared HERE rather than in the SDK:
// the shape is Google's, and one frozen into tako would turn their
// change into an API break.
type Incident struct {
	Incident struct {
		IncidentID    string `json:"incident_id"`
		PolicyName    string `json:"policy_name"`
		ConditionName string `json:"condition_name"`
		State         string `json:"state"`
		Summary       string `json:"summary"`
		URL           string `json:"url"`
	} `json:"incident"`
}

// There is deliberately no /pause switch.
//
// The first version had one: an atomic.Bool the API endpoint flipped and
// the worker read. It never did anything, and could not — under the
// partition rule a worker is its OWN service, so the flag was set in one
// process and read in another. The walk was passing a switch that was
// wired to nothing.
//
// A backlog needs no switch anyway: /flood with a hold builds one, which
// is what QueueBacklogged and QueueStalled watch.

// ── the switches ────────────────────────────────────────────────────

// BoomReq asks for a share of requests to fail.
type BoomReq struct {
	Rate float64 `query:"rate"`
}

// Boom fails a set fraction of requests — the switch behind
// RequestsErroring.
var Boom = tako.API("GET /boom", boom)

func boom(ctx tako.Ctx, req BoomReq) (string, error) {
	hits.Add(ctx, 1)
	if req.Rate > 0 && float64(time.Now().UnixNano()%100)/100 < req.Rate {
		return "", fmt.Errorf("boom: failing %.0f%% of requests on purpose", req.Rate*100)
	}
	return "ok", nil
}

// SlowReq asks for a slow response.
type SlowReq struct {
	Ms int `query:"ms"`
}

// Slow sleeps before answering — the switch behind RequestsSlow.
var Slow = tako.API("GET /slow", slow)

func slow(ctx tako.Ctx, req SlowReq) (string, error) {
	hits.Add(ctx, 1)
	d := time.Duration(req.Ms) * time.Millisecond
	time.Sleep(d)
	workMs.Observe(ctx, float64(d.Milliseconds()))
	return "slept", nil
}

// BurnReq asks for CPU to be consumed, not merely waited on.
type BurnReq struct {
	Ms int `query:"ms"`
}

// Burn spins the CPU — the switch behind ServicePercentile over
// cpu/utilizations.
//
// /slow will not do: it sleeps, and a sleeping request uses no CPU at
// all. The Z3 gate found that the hard way, driving four minutes of
// concurrent /slow and watching CPU sit at 1%. A saturation alert needs
// something that actually saturates.
var Burn = tako.API("GET /burn", burn)

func burn(ctx tako.Ctx, req BurnReq) (string, error) {
	hits.Add(ctx, 1)
	ms := req.Ms
	if ms == 0 {
		ms = 1000
	}
	x := spin(time.Duration(ms) * time.Millisecond)
	workMs.Observe(ctx, float64(ms))
	return fmt.Sprintf("burned %dms (%.0f)", ms, x), nil
}

// FloodReq asks for n messages, each held for HoldMs.
type FloodReq struct {
	N      int `query:"n"`
	HoldMs int `query:"hold_ms"`
	BurnMs int `query:"burn_ms"`
}

// Flood fills the live queue faster than the worker drains it — the
// switch behind QueueBacklogged and QueueStalled on the live queue.
var Flood = tako.API("POST /flood", flood)

func flood(ctx tako.Ctx, req FloodReq) (string, error) {
	n := req.N
	if n == 0 {
		n = 100
	}
	for i := 0; i < n; i++ {
		if err := work.Publish(ctx, Task{HoldMs: req.HoldMs, BurnMs: req.BurnMs}); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("queued %d", n), nil
}

// PoisonReq picks how the message dies.
type PoisonReq struct {
	// Kind is "hard" (tako.Permanent, dead-letters on attempt 1) or
	// "soft" (plain error, dead-letters after retries are exhausted).
	Kind string `query:"kind"`
}

// Poison sends work that cannot succeed — the switch behind
// QueueBacklogged and QueueStalled on the DEAD queue.
//
// Both kinds matter, and that is the point of having two. Pub/Sub's own
// dead-letter counter sees only the soft path, because tako answers a
// Permanent failure by publishing to the dead topic itself and acking.
// Watching the dead queue's DEPTH sees both.
var Poison = tako.API("POST /poison", poison)

func poison(ctx tako.Ctx, req PoisonReq) (string, error) {
	kind := req.Kind
	if kind == "" {
		kind = "hard"
	}
	if err := work.Publish(ctx, Task{Fail: kind}); err != nil {
		return "", err
	}
	return "poisoned: " + kind, nil
}

// IncidentReq asks for an incident that the subscriber cannot handle.
type IncidentReq struct {
	Fail bool `query:"fail"`
}

// Emit publishes an incident onto the alerts topic — the switch behind
// QueueBacklogged on a SUBSCRIBER's dead-letter queue.
//
// A subscriber has its own fate and its own dead topic, so one giving up
// says nothing about the others. This is the only way to prove that
// without breaking a real consumer.
var Emit = tako.API("POST /incident", emit)

func emit(ctx tako.Ctx, req IncidentReq) (string, error) {
	var in Incident
	in.Incident.PolicyName = "siren-self-test"
	in.Incident.State = "open"
	if req.Fail {
		in.Incident.State = "poison"
	}
	if err := alerts.Publish(ctx, in); err != nil {
		return "", err
	}
	return "emitted " + in.Incident.State, nil
}

// Sink receives incidents from the webhook channel, so an alert can be
// watched arriving without leaving the app.
//
// Note what this cannot do, and why the docs say so: a webhook pointed
// at your own service is unreachable in exactly the outage a page is
// for.
var Sink = tako.API("POST /incidents", sink)

func sink(ctx tako.Ctx, in Incident) (string, error) {
	ctx.Log().Warn("incident", "policy", in.Incident.PolicyName,
		"condition", in.Incident.ConditionName, "state", in.Incident.State)
	return "ok", nil
}

// ── the things that break ───────────────────────────────────────────

// Grind is the worker. MaxInstances 1 is what makes a flood back up
// rather than scale out, and is what ServiceAtCapacity watches.
var Grind = tako.Worker(work, grind, tako.Compute{
	Footprint: tako.Footprint{MaxInstances: 1, Concurrency: 1},
})

func grind(ctx tako.Ctx, t Task) error {
	if t.HoldMs > 0 {
		time.Sleep(time.Duration(t.HoldMs) * time.Millisecond)
	}
	if t.BurnMs > 0 {
		spin(time.Duration(t.BurnMs) * time.Millisecond)
	}
	switch t.Fail {
	case "hard":
		return tako.Permanent(fmt.Errorf("poisoned: this will never succeed"))
	case "soft":
		return fmt.Errorf("poisoned: failing until retries run out")
	}
	workMs.Observe(ctx, float64(t.HoldMs))
	return nil
}

// OnAlert routes incidents published to the alerts topic — the Pub/Sub
// channel's other end, in your own code.
var OnAlert = tako.Subscribe(alerts, onAlert)

func onAlert(ctx tako.Ctx, in Incident) error {
	if in.Incident.State == "poison" {
		return fmt.Errorf("onAlert: cannot route a poison incident")
	}
	ctx.Log().Warn("incident routed", "policy", in.Incident.PolicyName, "state", in.Incident.State)
	return nil
}

// Sweep is the healthy cron: CronMissed watches for it stopping.
var Sweep = tako.Cron("every 5m", sweep)

func sweep(ctx tako.Ctx) error {
	hits.Add(ctx, 1)
	return nil
}

// Flaky is the cron that always fails: CronFailed counts it.
var Flaky = tako.Cron("every 10m", flaky)

func flaky(ctx tako.Ctx) error {
	return fmt.Errorf("flaky: this cron never succeeds")
}

// Rebuild is a job that always exits non-zero: JobFailed counts it.
var (
	Rebuild   = tako.Job[RebuildArgs]("rebuild")
	Rebuilder = tako.Runner(Rebuild, rebuild)
)

// RebuildArgs is the job payload.
type RebuildArgs struct {
	OK bool `json:"ok"`
}

func rebuild(ctx tako.Ctx, a RebuildArgs) error {
	if a.OK {
		return nil
	}
	return fmt.Errorf("rebuild: failing on purpose")
}

// Ticker is the singleton: SingletonDown is the only check there is for
// a primitive whose whole promise is that one of it exists.
//
// There is deliberately no /kill switch, and the reason is a property of
// tako rather than an omission. A singleton that returns — with an error
// or without — is RESTARTED five seconds later, forever
// (pkg/runtime/job.go). That is the point of a singleton, so nothing the
// loop can do to itself will make its instance count reach zero.
//
// SingletonDown therefore fires on things OUTSIDE the app: the service
// deleted, scaled away, or failing to start. The walk stages it with
// `gcloud run services delete`, which is the honest reproduction.
var Ticker = tako.Singleton(tick)

func tick(ctx tako.Ctx) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(30 * time.Second):
			hits.Add(ctx, 1)
		}
	}
}
