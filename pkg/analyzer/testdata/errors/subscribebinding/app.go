package subscribebinding

import "github.com/gsandoval/tako"

type Evt struct{ N int }

// The binding is a constructor call, not a declared package-level var —
// the analyzer cannot name the topic.
var S = tako.Subscribe(tako.Topic[Evt]("inline"), handle)

func handle(ctx tako.Ctx, e Evt) error { return nil }
