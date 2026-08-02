package mixedingress

import "github.com/gsandoval/tako"

var List = tako.API("GET /api/products", list)
var Admin = tako.API("POST /api/restock", restock, tako.Route{Private: true})

func list(ctx tako.Ctx, _ struct{}) (string, error)    { return "[]", nil }
func restock(ctx tako.Ctx, _ struct{}) (string, error) { return "", nil }
