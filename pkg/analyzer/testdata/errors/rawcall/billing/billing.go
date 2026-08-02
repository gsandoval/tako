package billing

import "github.com/gsandoval/tako"

var Charge = tako.API("POST /charge", ChargeFn, tako.Route{Private: true})

// ChargeFn is exported by mistake — handler bodies are usually
// unexported, which makes this error impossible to write.
func ChargeFn(ctx tako.Ctx, _ struct{}) (string, error) { return "r1", nil }
