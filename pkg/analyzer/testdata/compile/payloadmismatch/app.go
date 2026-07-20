package payloadmismatch

import "github.com/gsandoval/tako"

// greet does not have the handler shape func(Ctx, Req) (Res, error) —
// this program must not compile. Payload shape is the compiler's job
// (interface rule 1), never the analyzer's.
var Greet = tako.API("GET /greet", greet)

func greet(name string) string { return name }

// A worker whose fn payload doesn't match the queue's T must also fail
// to compile — same rule, async shape.
var jobs = tako.Queue[int]("jobs")

var W = tako.Worker(jobs, wrongPayload)

func wrongPayload(ctx tako.Ctx, s string) error { return nil }

// A subscriber whose fn payload doesn't match the topic's T must also
// fail to compile.
var events = tako.Topic[int]("events")

var S = tako.Subscribe(events, wrongEvent)

func wrongEvent(ctx tako.Ctx, s string) error { return nil }
