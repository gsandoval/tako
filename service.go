package tako

// Service declares the package's app service — the deployable that runs
// its API, Page, and Cron handlers — so that service-level settings have
// a home that names them instead of riding whichever endpoint happened
// to be declared first.
//
// It is optional. A package with handlers and no Service declaration
// still deploys, as a service named after the package; declaring one
// names the service <package>-<var> instead, the same shape workers and
// subscribers already use.
//
//	var api = tako.Service(tako.WithDomain("api.example.com"))
//
// One per package. It accepts WithDomain, WithProfile and WithFootprint:
// every setting that describes the whole service rather than one route.
func Service(opts ...Option) *ServiceHandle { return &ServiceHandle{opts: opts} }

// ServiceHandle is the app service's declaration. It carries no methods:
// the declaration IS the value, read at analysis time.
type ServiceHandle struct{ opts []Option }
