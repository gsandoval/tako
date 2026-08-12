package statuspage

import "github.com/gsandoval/tako"

// The generated service registers the status page by name from outside
// this package, so an unexported one cannot be reached.
var status = tako.StatusPage(render)

func render(ctx tako.Ctx, s tako.Status) (tako.HTML, error) { return tako.HTML{}, nil }

var _ = status
