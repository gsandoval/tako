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
//	var api = tako.Service(tako.ServiceConfig{Domain: "api.example.com"})
//
// One per package. Its ServiceConfig carries Domain, Profile, Footprint and Traffic:
// every setting that describes the whole service rather than one route.
func Service(cfg ...ServiceConfig) *ServiceHandle { return &ServiceHandle{cfg: cfg} }

// ServiceHandle is the app service's declaration: the declaration IS
// the value, read at analysis time — and readable by the app that
// wrote it.
type ServiceHandle struct{ cfg []ServiceConfig }

// Domain reports the custom domain this service declared, or "" if it
// declared none.
//
// The domain is analysis-time configuration and stays that way: the
// analyzer reads it from the source literal, a variable is still
// refused (tako/config-literal), and the deploy plan is still computed
// without running the app. What this adds is the other direction —
// the app can read back what it declared, instead of repeating the
// string wherever it builds an absolute URL (canonical tags, RSS
// links, sitemap entries, share URLs).
//
//	var web = tako.Service(tako.ServiceConfig{Domain: "example.com"})
//
//	func canonical(path string) string { return "https://" + web.Domain() + path }
//
// The value is the declared one in every environment — it does not
// become localhost under tako dev or the run.app URL when deployed
// without a mapping. That is deliberate: the things absolute URLs are
// for name the public address of the site wherever the code happens to
// be running.
func (s *ServiceHandle) Domain() string {
	for _, c := range s.cfg {
		if c.Domain != "" {
			return c.Domain
		}
	}
	return ""
}
