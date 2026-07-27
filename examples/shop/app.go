// Package shop is the "website with an API" showcase, grown through
// W7–W10: a JSON api consumed by a generated TypeScript client
// (`tako gen client ts`), a static frontend bundled at deploy time
// (WithBuild — Node on the deploying machine, never in production),
// and email/password sign-in. The committed web/src/api.ts IS the
// generated client; regenerate with
//
//	tako gen client ts --out web/src/api.ts
//
// (a test pins freshness). v0 has no admin concept — any signed-in
// user stocks the shelf; later slices grow the shop into services.
package shop

import (
	"sort"
	"time"

	"shop/billing"

	"github.com/gsandoval/tako"
)

var (
	products = tako.Entity[Product]("products")
	orders   = tako.Entity[Order]("orders")

	// site serves the bundled frontend: WithBuild runs on the deploying
	// machine (tako dev / tako deploy), writes web/dist, and THAT is
	// what ships embedded in the binary.
	site = tako.Site("./web/dist",
		tako.WithBuild("cd web && npx --yes esbuild src/app.ts --bundle --format=esm --outfile=dist/app.js && cp index.html dist/", "./web/dist"))

	// auth: email/password — the one provider whose whole flow is
	// machine-testable (no OAuth popup). The declaration auto-enables
	// the provider in Identity Platform on deploy.
	auth = tako.Auth(tako.WithEmail())

	// settle is long-running compute (§2.7): a Cloud Run JOB in the
	// cloud — no request timeout to outlive — and a tracked in-process
	// execution locally.
	settle  = tako.Job[SettleParams]("settle")
	reports = tako.Entity[DailyReport]("reports")

	// sequences backs the invoicer: bookkeeping-grade invoice numbers
	// are strictly sequential — genuinely SERIAL work, which is what
	// makes Invoicer a Singleton and not a worker.
	sequences = tako.Entity[Sequence]("sequences")
)

type Product struct {
	ID         string `entity:"id" json:"id"`
	Name       string `json:"name"`
	PriceCents int    `json:"priceCents"`
}

type Order struct {
	ID         string    `entity:"id" json:"id"`
	Owner      string    `entity:"index" json:"owner"` // the buyer's uid; owners see only their own
	ProductID  string    `json:"productId"`
	Qty        int       `json:"qty"`
	TotalCents int       `json:"totalCents"`
	ReceiptID  string    `json:"receiptId"` // billing's receipt — the cross-service hop's proof
	Invoiced   bool      `entity:"index" json:"invoiced"`
	InvoiceNo  int64     `json:"invoiceNo"` // assigned by the invoicer singleton, strictly sequential
	Created    time.Time `entity:"index" json:"created"`
}

// SettleParams is the settle job's input. ThrottleMs simulates slow
// per-order work — the honest demo knob for "outlives any request
// deadline" (a real settlement would be slow on its own).
type SettleParams struct {
	Day        string `json:"day"`
	ThrottleMs int    `json:"throttleMs"`
}

// DailyReport is a settlement result: one record per run key.
type DailyReport struct {
	ID         string    `entity:"id" json:"id"`
	Orders     int       `json:"orders"`
	TotalCents int       `json:"totalCents"`
	SettledAt  time.Time `json:"settledAt"`
}

// Sequence is a named counter; the invoicer is its only writer.
type Sequence struct {
	ID   string `entity:"id" json:"id"`
	Next int64  `json:"next"`
}

var (
	ListProducts = tako.API("GET /api/products", listProducts)
	GetProduct   = tako.API("GET /api/products/{id}", getProduct)
	AddProduct   = tako.API("POST /api/products", addProduct, tako.WithAuth())
	PlaceOrder   = tako.API("POST /api/orders", placeOrder, tako.WithAuth())
	ListOrders   = tako.API("GET /api/orders", listOrders, tako.WithAuth())
	StartSettle  = tako.API("POST /api/settle", startSettle, tako.WithAuth())

	Settle   = tako.JobHandler(settle, runSettle)
	Invoicer = tako.Singleton("invoicer", assignInvoices)
)

func listProducts(ctx tako.Ctx, _ struct{}) ([]Product, error) {
	list, err := products.Query(ctx).All()
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

type GetProductReq struct {
	ID string `path:"id"`
}

func getProduct(ctx tako.Ctx, req GetProductReq) (Product, error) {
	p, err := products.Get(ctx, req.ID)
	if tako.IsNotFound(err) {
		return Product{}, tako.Errorf(404, "unknown product")
	}
	return p, err
}

type AddProductReq struct {
	Name       string `json:"name"`
	PriceCents int    `json:"priceCents"`
}

func addProduct(ctx tako.Ctx, req AddProductReq) (Product, error) {
	if req.Name == "" {
		return Product{}, tako.Errorf(422, "name must not be empty")
	}
	if req.PriceCents <= 0 {
		return Product{}, tako.Errorf(422, "priceCents must be positive")
	}
	p := Product{ID: tako.NewID(), Name: req.Name, PriceCents: req.PriceCents}
	if err := products.Put(ctx, p); err != nil {
		return Product{}, err
	}
	return p, nil
}

type PlaceOrderReq struct {
	ProductID string `json:"productId"`
	Qty       int    `json:"qty"`
}

func placeOrder(ctx tako.Ctx, req PlaceOrderReq) (Order, error) {
	if req.Qty <= 0 {
		return Order{}, tako.Errorf(422, "qty must be at least 1")
	}
	p, err := products.Get(ctx, req.ProductID)
	if tako.IsNotFound(err) {
		return Order{}, tako.Errorf(404, "unknown product")
	}
	if err != nil {
		return Order{}, err
	}
	o := Order{
		ID:         tako.NewID(),
		Owner:      ctx.User().ID,
		ProductID:  p.ID,
		Qty:        req.Qty,
		TotalCents: p.PriceCents * req.Qty,
		Created:    time.Now(),
	}
	// The typed cross-service hop (§2.12): in-process in dev and
	// tests, an OIDC-authenticated HTTP call in the cloud — same line.
	// billing sees THIS user (the session propagates), and its typed
	// errors surface here verbatim. No charge, no order.
	receipt, err := billing.Charge.Call(ctx, billing.ChargeReq{OrderID: o.ID, AmountCents: o.TotalCents})
	if err != nil {
		return Order{}, err
	}
	o.ReceiptID = receipt.ID
	if err := orders.Put(ctx, o); err != nil {
		return Order{}, err
	}
	return o, nil
}

func listOrders(ctx tako.Ctx, _ struct{}) ([]Order, error) {
	return orders.Query(ctx).Where("Owner", "=", ctx.User().ID).OrderBy("-Created").Limit(50).All()
}

type SettleReq struct {
	Day        string `json:"day"`
	ThrottleMs int    `json:"throttleMs"`
}

type SettleStarted struct {
	Started bool `json:"started"`
}

// startSettle HANDS OFF and returns immediately — the job is compute
// you walk away from; the report entity is where its result lands.
func startSettle(ctx tako.Ctx, req SettleReq) (SettleStarted, error) {
	if err := settle.Run(ctx, SettleParams{Day: req.Day, ThrottleMs: req.ThrottleMs}); err != nil {
		return SettleStarted{}, err
	}
	return SettleStarted{Started: true}, nil
}

// runSettle is the job body: sum every order into the day's report,
// however long that takes — there is no request deadline here.
func runSettle(ctx tako.Ctx, p SettleParams) error {
	all, err := orders.Query(ctx).All()
	if err != nil {
		return err
	}
	report := DailyReport{ID: reportKey(p.Day)}
	for _, o := range all {
		if p.ThrottleMs > 0 {
			time.Sleep(time.Duration(p.ThrottleMs) * time.Millisecond)
		}
		report.Orders++
		report.TotalCents += o.TotalCents
	}
	report.SettledAt = time.Now()
	return reports.Put(ctx, report)
}

func reportKey(day string) string {
	if day == "" {
		return "all"
	}
	return day
}

// assignInvoices is the singleton loop: sweep un-invoiced orders into
// strictly sequential numbers, forever, exactly once across every
// instance and revision (the lease is the guarantee).
func assignInvoices(ctx tako.Ctx) error {
	for {
		if _, err := invoiceSweep(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// invoiceSweep numbers pending orders in arrival order — one sweep,
// directly testable.
func invoiceSweep(ctx tako.Ctx) (int, error) {
	pending, err := orders.Query(ctx).Where("Invoiced", "=", false).Limit(100).All()
	if err != nil {
		return 0, err
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Created.Before(pending[j].Created) })
	for _, o := range pending {
		var no int64
		err := sequences.Tx(ctx, func(tx tako.EntityTx[Sequence]) error {
			seq, err := tx.Get("invoices")
			if err != nil && !tako.IsNotFound(err) {
				return err
			}
			seq.ID, seq.Next = "invoices", seq.Next+1
			no = seq.Next
			return tx.Put(seq)
		})
		if err != nil {
			return 0, err
		}
		if err := orders.Update(ctx, o.ID, func(o *Order) { o.Invoiced, o.InvoiceNo = true, no }); err != nil {
			return 0, err
		}
	}
	return len(pending), nil
}
