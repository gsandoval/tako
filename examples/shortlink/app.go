package shortlink

import (
	"errors"
	"time"

	"github.com/gsandoval/tako"
)

var (
	links  = tako.Entity[Link]("links")
	clicks = tako.Queue[Click]("clicks")
	seen   = tako.Entity[Click]("clicks-seen") // processed-event log: the idempotency ledger
)

// Link's schema comes from the struct: entity:"id" is the key,
// entity:"index" marks queryable fields.
type Link struct {
	Slug    string    `entity:"id" json:"slug"`
	URL     string    `json:"url"`
	Owner   string    `entity:"index" json:"owner"`
	Created time.Time `entity:"index" json:"created"`
	Hits    int       `json:"hits"`
}

// Click's ID is minted at publish time — it IS the idempotency key.
type Click struct {
	ID   string `entity:"id" json:"id"`
	Slug string `json:"slug"`
}

var (
	Create  = tako.API("POST /api/links", create)
	Resolve = tako.API("GET /api/links/{slug}", resolve)
	List    = tako.API("GET /api/links", list)

	CountClick = tako.Worker(clicks, countClick)
)

type CreateReq struct {
	Slug  string `json:"slug"`
	URL   string `json:"url"`
	Owner string `json:"owner"` // fallback until auth lands; a signed-in user wins
}

func create(ctx tako.Ctx, req CreateReq) (Link, error) {
	if req.Slug == "" || req.URL == "" {
		return Link{}, tako.Errorf(422, "slug and url must not be empty")
	}
	owner := req.Owner
	if u, ok := ctx.MaybeUser(); ok {
		owner = u.ID
	}
	link := Link{Slug: req.Slug, URL: req.URL, Owner: owner, Created: time.Now()}
	// Claim the slug atomically: check-then-put inside one transaction.
	err := links.Tx(ctx, func(tx tako.EntityTx[Link]) error {
		_, err := tx.Get(req.Slug)
		if err == nil {
			return tako.Errorf(409, "slug %q is taken", req.Slug)
		}
		if !tako.IsNotFound(err) {
			return err
		}
		return tx.Put(link)
	})
	if err != nil {
		return Link{}, err
	}
	return link, nil
}

type ResolveReq struct {
	Slug string `path:"slug"`
}

// resolve returns the link and records the click asynchronously — Hits
// is eventually consistent (drained queues, or moments later in dev).
func resolve(ctx tako.Ctx, req ResolveReq) (Link, error) {
	l, err := links.Get(ctx, req.Slug)
	if err != nil {
		return Link{}, err
	}
	return l, clicks.Publish(ctx, Click{ID: tako.NewID(), Slug: req.Slug})
}

var errAlreadyCounted = errors.New("already counted")

// countClick tallies a click exactly once. Delivery is at-least-once —
// chaos mode WILL deliver duplicates — so the event ID is claimed
// atomically before counting. Remove the claim and the chaos test fails
// with a reproduction seed.
func countClick(ctx tako.Ctx, c Click) error {
	err := seen.Tx(ctx, func(tx tako.EntityTx[Click]) error {
		if _, err := tx.Get(c.ID); err == nil {
			return errAlreadyCounted
		} else if !tako.IsNotFound(err) {
			return err
		}
		return tx.Put(c)
	})
	if errors.Is(err, errAlreadyCounted) {
		return nil // duplicate delivery: ack, count nothing
	}
	if err != nil {
		return err
	}
	return links.Update(ctx, c.Slug, func(l *Link) { l.Hits++ })
}

type ListReq struct {
	Owner string `query:"owner"`
}

func list(ctx tako.Ctx, req ListReq) ([]Link, error) {
	q := links.Query(ctx)
	if req.Owner != "" {
		q = q.Where("Owner", "=", req.Owner)
	}
	return q.OrderBy("-Created").Limit(100).All()
}
