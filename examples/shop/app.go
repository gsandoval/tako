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
	Created    time.Time `entity:"index" json:"created"`
}

var (
	ListProducts = tako.API("GET /api/products", listProducts)
	GetProduct   = tako.API("GET /api/products/{id}", getProduct)
	AddProduct   = tako.API("POST /api/products", addProduct, tako.WithAuth())
	PlaceOrder   = tako.API("POST /api/orders", placeOrder, tako.WithAuth())
	ListOrders   = tako.API("GET /api/orders", listOrders, tako.WithAuth())
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
	if err := orders.Put(ctx, o); err != nil {
		return Order{}, err
	}
	return o, nil
}

func listOrders(ctx tako.Ctx, _ struct{}) ([]Order, error) {
	return orders.Query(ctx).Where("Owner", "=", ctx.User().ID).OrderBy("-Created").Limit(50).All()
}
