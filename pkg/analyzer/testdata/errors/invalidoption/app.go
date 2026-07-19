package invalidoption

import (
	"time"

	"github.com/gsandoval/tako"
)

var Hello = tako.API("GET /hello", hello, tako.WithTTL(time.Minute))

func hello(ctx tako.Ctx, _ struct{}) (string, error) { return "hi", nil }
