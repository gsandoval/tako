package secretname

import "github.com/gsandoval/tako"

var keyName = "notifier-key"

var notifier = tako.Secret(keyName)

var Ping = tako.API("GET /api/ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) {
	return notifier.Value(), nil
}
