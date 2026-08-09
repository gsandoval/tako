package docserver

// The api section lists every exported symbol in the tako package with
// its signature, from docs/api/surface.txt — which is the API-freeze
// test's own lock file, pinned byte-for-byte (see docs/surface_test.go).
// The page cannot silently lose a symbol, because the thing that fails
// when the surface changes is the same thing that feeds it.
//
// What the lock does NOT carry is where each symbol is explained, so
// symbolPage below says. It is a table someone maintains, which is the
// point: a new exported symbol with no reference page fails
// TestEveryExportedSymbolHasAReferencePage, and an API change is
// already its own task with its own review.

import (
	"fmt"
	"regexp"
	"strings"
)

// A Symbol is one line of the SDK lock: a type, function, constant or
// variable, with the methods that hang off a type.
type Symbol struct {
	Kind    string // "type", "func", "const", "var"
	Name    string // "EntityHandle"
	Decl    string // the whole declaration, as the lock renders it
	Page    string // "entity.md", or "" when no reference page covers it
	Methods []Symbol
}

// A Group is one heading on the api page: a section of the reference,
// holding the pages in it, holding the symbols they document.
type Group struct {
	Section string
	Pages   []GroupPage
}

// A GroupPage is one reference page's symbols.
type GroupPage struct {
	Title   string // the page's H1, or the file name if it has none
	Route   string // "/reference/entity", empty when there is no page
	Symbols []Symbol
}

// undocumented heads the group for symbols no reference page covers.
const undocumented = "Not on a reference page yet"

// embeddedIn names the types whose methods the lock files under some
// other receiver. Ctx embeds context.Context, so its four inherited
// methods render as (context.Context).Deadline and friends — which are
// Ctx's methods to anyone reading.
var embeddedIn = map[string]string{"context.Context": "Ctx"}

// parseSurface reads the lock. The lock is sorted as text, so every
// indented method line sorts ahead of every declaration and a method is
// nowhere near its type: the receiver is what says where it belongs.
func parseSurface(src string) ([]Symbol, error) {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")

	var out []Symbol
	at := map[string]int{}
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, " ") {
			continue
		}
		s := symbol(line)
		s.Page = symbolPage[s.Name]
		at[s.Name] = len(out)
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("surface.txt has no declarations")
	}

	for i, line := range lines {
		if !strings.HasPrefix(line, " ") || strings.TrimSpace(line) == "" {
			continue
		}
		decl := strings.TrimSpace(line)
		recv := receiver(decl)
		if alias, ok := embeddedIn[recv]; ok {
			recv = alias
		}
		idx, ok := at[recv]
		if !ok {
			return nil, fmt.Errorf("surface.txt:%d: a method on %q, which is not in the lock — "+
				"if it arrived through an embedded type, name it in embeddedIn", i+1, recv)
		}
		m := symbol(decl)
		m.Page = out[idx].Page
		out[idx].Methods = append(out[idx].Methods, m)
	}
	return out, nil
}

// receiver is the type a method line hangs off: "*CacheHandle[T]" and
// "PutPolicy" both name the symbol the lock declares.
func receiver(decl string) string {
	_, rest, ok := strings.Cut(decl, "(")
	if !ok {
		return ""
	}
	recv, _, ok := strings.Cut(rest, ").")
	if !ok {
		return ""
	}
	recv = strings.TrimPrefix(recv, "*")
	if i := strings.Index(recv, "["); i >= 0 {
		recv = recv[:i]
	}
	return recv
}

// symbol splits a declaration into its kind and name. A name ends at
// the first thing that cannot be part of one: a type parameter list, a
// signature, or the space before a type.
func symbol(decl string) Symbol {
	kind, rest, ok := strings.Cut(decl, " ")
	if !ok {
		return Symbol{Decl: decl}
	}
	name := rest
	if i := strings.IndexAny(name, "[( "); i >= 0 {
		name = name[:i]
	}
	// Methods render as "func (*EntityHandle[T]).Get(...)"; the name is
	// what follows the receiver.
	if strings.HasPrefix(rest, "(") {
		if _, after, ok := strings.Cut(rest, ")."); ok {
			name = after
			if i := strings.IndexAny(name, "[( "); i >= 0 {
				name = name[:i]
			}
		}
	}
	return Symbol{Kind: kind, Name: name, Decl: decl}
}

var (
	refHeading = regexp.MustCompile(`^## (.+)$`)
	// Only a bullet that STARTS with a page link is a page listing.
	// The reference's own prose links to pages mid-sentence, and those
	// are not section membership.
	refBullet = regexp.MustCompile(`^- \[([A-Za-z0-9._-]+\.md)\]\(`)
)

// sections reads the reference's grouping out of its README, rather
// than restating it here. That grouping is a considered one, and a
// second ordering would be a second thing to keep true.
func sections(readme string) []struct {
	Name  string
	Pages []string
} {
	type section = struct {
		Name  string
		Pages []string
	}
	var out []section
	current := -1
	for _, line := range strings.Split(readme, "\n") {
		if m := refHeading.FindStringSubmatch(line); m != nil {
			out = append(out, section{Name: strings.TrimSpace(m[1])})
			current = len(out) - 1
			continue
		}
		if m := refBullet.FindStringSubmatch(line); m != nil && current >= 0 {
			out[current].Pages = append(out[current].Pages, m[1])
		}
	}
	// A heading with no page bullets is prose, not a group.
	kept := out[:0]
	for _, s := range out {
		if len(s.Pages) > 0 {
			kept = append(kept, s)
		}
	}
	return kept
}

// referenceOrder is the reference's nav: its index, then its pages in
// the order the index groups them, then anything the index forgot —
// listed rather than dropped, because a page nothing links to is still
// a page someone wrote.
func referenceOrder(readme string, byRoute map[string]*Page) []*Page {
	var out []*Page
	listed := map[string]bool{}
	add := func(p *Page) {
		if p == nil || listed[p.Route] {
			return
		}
		listed[p.Route] = true
		out = append(out, p)
	}
	add(byRoute["/reference/"])
	for _, s := range sections(readme) {
		for _, file := range s.Pages {
			add(byRoute[routeFor("reference/"+file)])
		}
	}
	var rest []*Page
	for _, p := range byRoute {
		if p.Section == SectionReference && !listed[p.Route] {
			rest = append(rest, p)
		}
	}
	sortPages(rest)
	return append(out, rest...)
}

// group lays the symbols out the way the reference lays out its pages.
func group(symbols []Symbol, readme string, byRoute map[string]*Page) []Group {
	byPage := map[string][]Symbol{}
	for _, s := range symbols {
		byPage[s.Page] = append(byPage[s.Page], s)
	}
	var out []Group
	for _, s := range sections(readme) {
		g := Group{Section: s.Name}
		for _, file := range s.Pages {
			syms := byPage[file]
			if len(syms) == 0 {
				// Pages document behaviour that has no symbol of its
				// own — domains is a field, cli is a command surface.
				continue
			}
			gp := GroupPage{Title: file, Symbols: syms}
			if p := byRoute[routeFor("reference/"+file)]; p != nil {
				gp.Title, gp.Route = p.Title, p.Route
			}
			g.Pages = append(g.Pages, gp)
		}
		if len(g.Pages) > 0 {
			out = append(out, g)
		}
	}
	if orphans := byPage[""]; len(orphans) > 0 {
		out = append(out, Group{
			Section: undocumented,
			Pages:   []GroupPage{{Title: "no page explains these yet", Symbols: orphans}},
		})
	}
	return out
}

func sortPages(pages []*Page) {
	for i := 1; i < len(pages); i++ {
		for j := i; j > 0 && pages[j].Name < pages[j-1].Name; j-- {
			pages[j], pages[j-1] = pages[j-1], pages[j]
		}
	}
}

// symbolPage maps every exported symbol to the reference page that
// explains it. Methods follow their type.
//
// A symbol mapped to "" is one no single page documents; it is printed
// under its own heading on the api page rather than hidden, so the gap
// is visible to a reader instead of only to us.
var symbolPage = map[string]string{
	// HTTP
	"API":    "api.md",
	"Ctx":    "api.md",
	"Errorf": "api.md",
	// NotFound and IsNotFound are the same story as Errorf: the status
	// helpers a handler returns.
	"NotFound":          "api.md",
	"IsNotFound":        "api.md",
	"Route":             "api.md",
	"Page":              "pages.md",
	"HTML":              "pages.md",
	"Redirect":          "pages.md",
	"RedirectPermanent": "pages.md",
	"Site":              "site.md",
	"SiteHandle":        "site.md",
	"SiteConfig":        "site.md",
	// The pages tako renders on the app's behalf live with the site,
	// because that is what they wear.
	"StatusPage":    "site.md",
	"Status":        "site.md",
	"StatusHandle":  "site.md",
	"Build":         "site.md",
	"Service":       "service.md",
	"ServiceHandle": "service.md",
	"ServiceConfig": "service.md",
	// Where the service is running is a fact about the deployment, so
	// it is documented with the deployable rather than with a route.
	"Environment": "service.md",
	"Env":         "service.md",
	"EnvLocal":    "service.md",
	"EnvCloud":    "service.md",

	// Data
	"Entity":          "entity.md",
	"EntityHandle":    "entity.md",
	"EntityQuery":     "entity.md",
	"EntityPage":      "entity.md",
	"EntityTx":        "entity.md",
	"Iter":            "entity.md",
	"NewID":           "entity.md",
	"Cache":           "cache.md",
	"CacheHandle":     "cache.md",
	"Secret":          "observability.md",
	"SecretHandle":    "observability.md",
	"Counter":         "observability.md",
	"CounterHandle":   "observability.md",
	"Histogram":       "observability.md",
	"HistogramHandle": "observability.md",

	// Alerts: the constructors, the readings, and the vocabulary they
	// are written in.
	"Alert":             "alerts.md",
	"AlertHandle":       "alerts.md",
	"AlertEmail":        "alerts.md",
	"AlertWebhook":      "alerts.md",
	"AlertTopic":        "alerts.md",
	"ChannelHandle":     "alerts.md",
	"Condition":         "alerts.md",
	"Metric":            "alerts.md",
	"Target":            "alerts.md",
	"Consumer":          "alerts.md",
	"Window":            "alerts.md",
	"QueueWatch":        "alerts.md",
	"Requests":          "alerts.md",
	"RequestsHandle":    "alerts.md",
	"RequestMs":         "alerts.md",
	"RequestMsHandle":   "alerts.md",
	"ServiceMetric":     "alerts.md",
	"ServicePercentile": "alerts.md",
	"ServiceInstances":  "alerts.md",
	"PlatformMetric":    "alerts.md",
	"QueueMetric":       "alerts.md",
	"WhenAbove":         "alerts.md",
	"WhenBelow":         "alerts.md",
	"WhenAtLeast":       "alerts.md",
	"WhenAbsent":        "alerts.md",
	"RequestsErroring":  "alerts.md",
	"RequestsSlow":      "alerts.md",
	"ServiceAtCapacity": "alerts.md",
	"QueueBacklogged":   "alerts.md",
	"QueueStalled":      "alerts.md",
	"CronFailed":        "alerts.md",
	"CronMissed":        "alerts.md",
	"JobFailed":         "alerts.md",
	"SingletonDown":     "alerts.md",

	// Async
	"Queue":        "queue.md",
	"QueueHandle":  "queue.md",
	"Worker":       "queue.md",
	"Delivery":     "queue.md",
	"Permanent":    "queue.md",
	"Topic":        "topic.md",
	"TopicHandle":  "topic.md",
	"Subscribe":    "topic.md",
	"EventSource":  "topic.md",
	"Bucket":       "bucket.md",
	"BucketHandle": "bucket.md",
	"BucketEvent":  "bucket.md",
	"Blob":         "bucket.md",
	"ObjectInfo":   "bucket.md",
	"PutPolicy":    "bucket.md",
	"Cron":         "cron.md",
	"CronConfig":   "cron.md",
	"Lock":         "lock.md",
	"LockHandle":   "lock.md",
	// The value Worker, Subscribe, Runner, Cron and Singleton all
	// return. Filed under Async because four of the five are.
	"Handler": "handler.md",

	// Compute
	"Job":             "job.md",
	"JobHandle":       "job.md",
	"Runner":          "job.md",
	"RunnerConfig":    "job.md",
	"Singleton":       "singleton.md",
	"SingletonConfig": "singleton.md",
	"Endpoint":        "calls.md",
	"Profile":         "footprint.md",
	"ProfileDefault":  "footprint.md",
	"ProfileLatency":  "footprint.md",
	"ProfileBatch":    "footprint.md",
	"Footprint":       "footprint.md",
	"Compute":         "footprint.md",
	"Traffic":         "footprint.md",
	"Toggle":          "footprint.md",
	"ToggleInherit":   "footprint.md",
	"ToggleOn":        "footprint.md",
	"ToggleOff":       "footprint.md",

	// Edges
	"Auth":       "auth.md",
	"AuthHandle": "auth.md",
	"AuthConfig": "auth.md",
	"User":       "auth.md",
	// Authorization sits with authentication: Route.Allow is the other
	// half of the door tako.Auth opens.
	"Policy":       "auth.md",
	"PolicyHandle": "auth.md",
	"Deny":         "auth.md",
	"IsDenied":     "auth.md",
	"Role":         "auth.md",
	"UsersHandle":  "auth.md",
	"NewUser":      "auth.md",
	"UsersPage":    "auth.md",
	"UserQuery":    "auth.md",
	"IsEmailTaken": "auth.md",
	"RoleHandle":   "auth.md",
	"RequireRole":  "auth.md",
	"NewTestApp":   "testing.md",
	"TestApp":      "testing.md",
}
