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
	"sync/atomic"
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

// paused stops the worker acking, so the live queue backs up without
// anything failing. It is the difference between "stalled" and "dying".
var paused atomic.Bool

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

// FloodReq asks for n messages, each held for HoldMs.
type FloodReq struct {
	N      int `query:"n"`
	HoldMs int `query:"hold_ms"`
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
		if err := work.Publish(ctx, Task{HoldMs: req.HoldMs}); err != nil {
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

// Pause stops the worker acking without failing anything, so the live
// queue ages while nothing dead-letters.
var Pause = tako.API("POST /pause", pause)

func pause(ctx tako.Ctx, _ struct{}) (string, error) {
	paused.Store(!paused.Load())
	return fmt.Sprintf("paused=%v", paused.Load()), nil
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
	for paused.Load() {
		time.Sleep(time.Second)
	}
	if t.HoldMs > 0 {
		time.Sleep(time.Duration(t.HoldMs) * time.Millisecond)
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
