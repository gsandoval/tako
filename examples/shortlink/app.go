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

	// Expiries ride WithDelay (T3): creating a link with a TTL publishes
	// its own expiry, delivered when it comes due — Cloud Tasks in the
	// cloud, a timer (or the test clock) locally.
	expiries = tako.Queue[Expiry]("expiries")

	// The archive walks dead links at a POLITE pace: RateLimit caps
	// dispatch at 2/s everywhere — Cloud Tasks enforces it in the cloud,
	// the local dispatcher paces to the same number. Archiving is
	// idempotent and never urgent, so it gets fewer, slower retries than
	// the framework default.
	archive = tako.Queue[Expiry]("archive", tako.WithDelivery(tako.Delivery{
		RateLimit:   2,
		MaxAttempts: 5,
		MinBackoff:  30 * time.Second,
		MaxBackoff:  10 * time.Minute,
	}))
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
	Expire     = tako.Worker(expiries, expire)
	Archive    = tako.Worker(archive, archiveLink)
)

// Expiry names one link due for deletion; Created guards against a
// slug being re-claimed between publish and delivery.
type Expiry struct {
	ID      string    `entity:"id" json:"id"`
	Slug    string    `json:"slug"`
	Created time.Time `json:"created"`
}

type CreateReq struct {
	Slug  string `json:"slug"`
	URL   string `json:"url"`
	Owner string `json:"owner"` // fallback until auth lands; a signed-in user wins
	// TTLSeconds > 0 makes the link self-destruct after that long.
	TTLSeconds int `json:"ttlSeconds"`
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
	if req.TTLSeconds > 0 {
		ttl := time.Duration(req.TTLSeconds) * time.Second
		if err := expiries.Publish(ctx,
			Expiry{ID: tako.NewID(), Slug: link.Slug, Created: link.Created},
			tako.WithDelay(ttl)); err != nil {
			return Link{}, err
		}
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

// expire deletes a due link — unless the slug was deleted and
// re-claimed meanwhile (Created differs): expiry is a claim about ONE
// link, not about whoever holds the slug now. The dead link then walks
// to the rate-limited archive.
func expire(ctx tako.Ctx, e Expiry) error {
	l, err := links.Get(ctx, e.Slug)
	if tako.IsNotFound(err) {
		return nil // already gone: expiry is idempotent
	}
	if err != nil {
		return err
	}
	if !l.Created.Equal(e.Created) {
		return nil // the slug was re-claimed; the new link owns it
	}
	if err := links.Delete(ctx, e.Slug); err != nil {
		return err
	}
	return archive.Publish(ctx, e)
}

// archived is the tombstone ledger the paced archive worker fills.
var archived = tako.Entity[Expiry]("archived")

func archiveLink(ctx tako.Ctx, e Expiry) error {
	return archived.Put(ctx, e)
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
