// Package cachesignin holds the one combination Route refuses.
//
// A shared cache holding a signed-in page hands it to whoever asks
// next. Nothing about that failure is visible from the app: the tests
// pass, the page renders, and one day a reader sees somebody else's
// draft.
package cachesignin

import (
	"time"

	"github.com/gsandoval/tako"
)

var Dashboard = tako.Page("GET /dashboard", dashboard, tako.Route{
	SignIn: true,
	MaxAge: 5 * time.Minute,
})

func dashboard(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
	return tako.HTML{}, nil
}

// A MaxAge that is not a constant cannot be baked into a header at
// build time.
var Feed = tako.Page("GET /feed", feed, tako.Route{MaxAge: window()})

func window() time.Duration { return time.Minute }

func feed(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
	return tako.HTML{}, nil
}
