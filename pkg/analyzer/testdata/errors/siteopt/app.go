package siteopt

import (
	"time"

	"github.com/gsandoval/tako"
)

var site = tako.Site("./web", tako.WithTTL(time.Minute), tako.WithCDN())
