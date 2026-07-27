// Package billing is shop's second SERVICE — a package, not YAML
// (spec §2.12): mkdir made it, the analyzer partitions it, and the
// deploy gives it its own Cloud Run service, SA, and IAM-only ingress.
// v0 charges are a deterministic fake processor; a real one would live
// behind the same endpoint.
package billing

import (
	"github.com/gsandoval/tako"
)

// Charge is the RPC surface: an EXPORTED Endpoint var, callable from
// other service packages as billing.Charge.Call(ctx, req) — typed,
// visible at the call site, no client code to maintain. WithPrivate
// closes the front door: only granted service identities invoke
// (browsers and curl get 403 from Cloud Run itself).
var Charge = tako.API("POST /charge", charge, tako.WithPrivate())

type ChargeReq struct {
	OrderID     string `json:"orderId"`
	AmountCents int    `json:"amountCents"`
}

type Receipt struct {
	ID          string `json:"id"`
	OrderID     string `json:"orderId"`
	AmountCents int    `json:"amountCents"`
	ChargedBy   string `json:"chargedBy"` // the PROPAGATED user — alice's session crosses the hop
}

// charge validates and "processes" the payment. Its typed errors cross
// the wire verbatim: this 422 is placeOrder's 422 to handle, never a
// mystery 500.
func charge(ctx tako.Ctx, req ChargeReq) (Receipt, error) {
	if req.AmountCents <= 0 {
		return Receipt{}, tako.Errorf(422, "amount must be positive")
	}
	if req.OrderID == "" {
		return Receipt{}, tako.Errorf(422, "orderId must not be empty")
	}
	by := ""
	if u, ok := ctx.MaybeUser(); ok {
		by = u.ID
	}
	return Receipt{ID: tako.NewID(), OrderID: req.OrderID, AmountCents: req.AmountCents, ChargedBy: by}, nil
}
