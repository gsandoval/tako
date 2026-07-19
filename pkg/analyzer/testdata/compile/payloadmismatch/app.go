package payloadmismatch

import "github.com/gsandoval/tako"

// greet does not have the handler shape func(Ctx, Req) (Res, error) —
// this program must not compile. Payload shape is the compiler's job
// (interface rule 1), never the analyzer's.
var Greet = tako.API("GET /greet", greet)

func greet(name string) string { return name }
