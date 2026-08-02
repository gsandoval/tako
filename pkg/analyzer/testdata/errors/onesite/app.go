package onesite

import "github.com/gsandoval/tako"

var site = tako.Site("./web")
var admin = tako.Site("./web", tako.SiteConfig{SPA: true})
