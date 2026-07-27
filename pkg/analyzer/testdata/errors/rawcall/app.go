package rawcall

import (
	"rawcall/billing"

	"github.com/gsandoval/tako"
)

var Checkout = tako.API("POST /api/checkout", checkout)

func checkout(ctx tako.Ctx, _ struct{}) (string, error) {
	// The raw function, not the Endpoint var: the RPC boundary vanishes.
	return billing.ChargeFn(ctx, struct{}{})
}
