package singletonmax

import "github.com/gsandoval/tako"

// The lease is what makes a singleton exactly-one, so a max above 1 buys
// nothing but instances that pay to lose the race. CPU/Memory/Timeout
// are the user's; the shape that defines the primitive is not.
var Loop = tako.Singleton(loop,
	tako.SingletonConfig{Footprint: tako.Footprint{Memory: "1Gi", MaxInstances: 3}})

func loop(ctx tako.Ctx) error { return nil }
