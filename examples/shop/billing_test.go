package shop

// The W9 contract, in-process half: billing.Charge is a typed call —
// its errors cross verbatim, the user rides the ctx, and the "hop" in
// dev and tests is a plain function call a debugger steps into.

import (
	"strings"
	"testing"

	"shop/billing"

	tako "github.com/gsandoval/tako"
)

func TestChargeIsATypedCall(t *testing.T) {
	app := tako.NewTestApp(t)
	ctx := app.Ctx(app.SignIn("alice"))

	receipt, err := billing.Charge.Call(ctx, billing.ChargeReq{OrderID: "o1", AmountCents: 500})
	if err != nil || receipt.AmountCents != 500 || receipt.ID == "" {
		t.Fatalf("charge = %+v, %v", receipt, err)
	}
	// The user crossed the call: billing saw alice, not anonymity.
	if receipt.ChargedBy != "alice" {
		t.Fatalf("chargedBy = %q — the caller's identity must reach the callee", receipt.ChargedBy)
	}
}

func TestChargeErrorsAreTheCallers(t *testing.T) {
	app := tako.NewTestApp(t)
	ctx := app.Ctx()

	_, err := billing.Charge.Call(ctx, billing.ChargeReq{OrderID: "o1", AmountCents: 0})
	he, ok := err.(interface{ HTTPStatus() int })
	if !ok || he.HTTPStatus() != 422 || !strings.Contains(err.Error(), "amount must be positive") {
		t.Fatalf("err = %v — billing's 422 must be the caller's 422", err)
	}
}

func TestAnonymousCallerIsHonestlyAnonymous(t *testing.T) {
	app := tako.NewTestApp(t)
	ctx := app.Ctx() // a worker or cron would look like this

	receipt, err := billing.Charge.Call(ctx, billing.ChargeReq{OrderID: "o2", AmountCents: 100})
	if err != nil || receipt.ChargedBy != "" {
		t.Fatalf("receipt = %+v, %v — no session means no user, honestly", receipt, err)
	}
}
