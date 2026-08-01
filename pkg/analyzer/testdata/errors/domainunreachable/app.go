package domainunreachable

import "github.com/gsandoval/tako"

// A site with a domain in a package whose only handler is a worker:
// nothing here serves public traffic, so the name would answer nothing.
var (
	jobs = tako.Queue[string]("jobs")
	site = tako.Site("./web", tako.WithDomain("blog.example.com"))
	Work = tako.Worker(jobs, work)
)

func work(ctx tako.Ctx, _ string) error { return nil }
