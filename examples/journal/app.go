// Package journal is the website-with-an-API showcase (spec Part 3b),
// grown feature by feature through Stage 3: W1 the static shell, W2
// pages and forms, W3 htmx fragments, W4 authored markdown, W5 auth —
// gated whole at W6. Right now it is the smallest possible tako
// website: one declaration, zero handlers.
package journal

import "github.com/gsandoval/tako"

// site serves ./web embedded in the service binary: correct caching
// (fingerprinted assets immutable, HTML no-cache, ETags), conservative
// security headers, and a redirect from the old address of the about
// page. Templates and pages join in W2.
var site = tako.Site("./web", tako.WithRedirect("/about-us", "/about"))
