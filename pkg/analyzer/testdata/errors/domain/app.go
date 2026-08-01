package domain

import "github.com/gsandoval/tako"

// Every way a domain string can fail the shape rules. Backend limits
// (Cloud Run's 64-char cap, its region list) are deploy's to enforce —
// the analyzer only asks whether this is a domain name at all.
var (
	Scheme   = tako.API("GET /a", ping, tako.WithDomain("https://blog.example.com/"))
	Port     = tako.API("GET /b", ping, tako.WithDomain("blog.example.com:8080"))
	Trailing = tako.API("GET /c", ping, tako.WithDomain("blog.example.com."))
	Bare     = tako.API("GET /d", ping, tako.WithDomain("blog"))
	Wildcard = tako.API("GET /e", ping, tako.WithDomain("*.example.com"))
	Upper    = tako.API("GET /f", ping, tako.WithDomain("Blog.Example.com"))
	Empty    = tako.API("GET /g", ping, tako.WithDomain(""))
	Label    = tako.API("GET /h", ping, tako.WithDomain("blog..example.com"))
	Hyphen   = tako.API("GET /i", ping, tako.WithDomain("-blog.example.com"))
	Char     = tako.API("GET /j", ping, tako.WithDomain("blog_1.example.com"))
)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
