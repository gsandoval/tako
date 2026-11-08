package projdocs

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/gsandoval/tako/internal/schedule"
	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/provision"
	"github.com/gsandoval/tako/pkg/schemagen"
)

var sliceTitles = map[string]string{
	"architecture": "Architecture",
	"apis":         "APIs & Routes",
	"schedules":    "Schedules",
	"resources":    "Resources",
	"roles":        "Roles & Access",
	"footprints":   "Footprints & Traffic",
	"permissions":  "IAM Permissions",
}

func sliceTitle(s string) string {
	if t, ok := sliceTitles[s]; ok {
		return t
	}
	return s
}

func pageShell(g *analyzer.AppGraph, opts Options, active, title, bodyHTML string) []byte {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("  <meta charset=\"utf-8\">\n")
	b.WriteString("  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	fmt.Fprintf(&b, "  <title>%s — %s (%s)</title>\n",
		html.EscapeString(title), html.EscapeString(g.Module), html.EscapeString(opts.Name))
	b.WriteString(`  <style>
    :root {
      --bg: #0f172a;
      --panel: #1e293b;
      --border: #334155;
      --text: #f8fafc;
      --muted: #94a3b8;
      --accent: #38bdf8;
      --badge-bg: #1e3a8a;
      --badge-text: #bfdbfe;
      --warn-bg: #78350f;
      --warn-text: #fde68a;
      --ok-bg: #064e3b;
      --ok-text: #a7f3d0;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background: var(--bg);
      color: var(--text);
      display: flex;
      min-height: 100vh;
      line-height: 1.5;
    }
    aside {
      width: 260px;
      background: var(--panel);
      border-right: 1px solid var(--border);
      padding: 1.5rem 1rem;
      flex-shrink: 0;
    }
    .brand {
      font-weight: 700;
      font-size: 1.05rem;
      margin-bottom: 0.25rem;
      word-break: break-all;
    }
    .meta {
      font-size: 0.8rem;
      color: var(--muted);
      margin-bottom: 1.25rem;
    }
    nav a {
      display: block;
      color: var(--muted);
      text-decoration: none;
      padding: 0.45rem 0.65rem;
      border-radius: 6px;
      margin-bottom: 0.2rem;
      font-size: 0.92rem;
    }
    nav a:hover, nav a.active {
      background: rgba(56, 189, 248, 0.14);
      color: var(--accent);
    }
    nav .divider {
      height: 1px;
      background: var(--border);
      margin: 0.85rem 0;
    }
    main {
      flex: 1;
      padding: 2rem 2.5rem;
      max-width: 1120px;
    }
    h1 { margin-top: 0; font-size: 1.65rem; }
    h2 { margin-top: 2rem; font-size: 1.2rem; border-bottom: 1px solid var(--border); padding-bottom: 0.4rem; }
    .cards {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
      gap: 1rem;
      margin: 1.25rem 0;
    }
    .card {
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 1rem 1.15rem;
    }
    .card h3 { margin: 0 0 0.35rem 0; font-size: 0.95rem; color: var(--muted); }
    .card .stat { font-size: 1.5rem; font-weight: 700; color: var(--text); }
    .card a { color: var(--accent); text-decoration: none; font-size: 0.85rem; }
    table {
      width: 100%;
      border-collapse: collapse;
      margin: 1rem 0 1.75rem 0;
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 8px;
      overflow: hidden;
      font-size: 0.9rem;
    }
    th, td {
      text-align: left;
      padding: 0.65rem 0.85rem;
      border-bottom: 1px solid var(--border);
      vertical-align: top;
    }
    th {
      color: var(--muted);
      font-weight: 600;
      font-size: 0.8rem;
      text-transform: uppercase;
      letter-spacing: 0.04em;
    }
    code, pre {
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
      font-size: 0.85rem;
    }
    pre {
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 1rem;
      overflow-x: auto;
    }
    .badge {
      display: inline-block;
      padding: 0.15rem 0.5rem;
      border-radius: 999px;
      font-size: 0.75rem;
      font-weight: 600;
      background: var(--badge-bg);
      color: var(--badge-text);
      margin-right: 0.35rem;
    }
    .badge.warn { background: var(--warn-bg); color: var(--warn-text); }
    .badge.ok { background: var(--ok-bg); color: var(--ok-text); }
  </style>
</head>
<body>
  <aside>
`)
	fmt.Fprintf(&b, "    <div class=\"brand\">%s</div>\n", html.EscapeString(g.Module))
	vis := "private"
	if opts.Public {
		vis = "public"
	}
	fmt.Fprintf(&b, "    <div class=\"meta\">docs: <code>%s</code> &middot; level: <code>%s</code> &middot; %s</div>\n",
		html.EscapeString(opts.Name), html.EscapeString(opts.Level), vis)
	b.WriteString("    <nav>\n")
	activeClass := func(id string) string {
		if id == active {
			return " class=\"active\""
		}
		return ""
	}
	fmt.Fprintf(&b, "      <a href=\"index.html\"%s>Overview</a>\n", activeClass("overview"))
	for _, s := range opts.Slices {
		fmt.Fprintf(&b, "      <a href=\"%s.html\"%s>%s</a>\n",
			html.EscapeString(s), activeClass(s), html.EscapeString(sliceTitle(s)))
	}
	b.WriteString("      <div class=\"divider\"></div>\n")
	if opts.HasSlice("apis") {
		b.WriteString("      <a href=\"openapi.json\">OpenAPI 3.1 JSON</a>\n")
	}
	b.WriteString("      <a href=\"llms.txt\">llms.txt</a>\n")
	b.WriteString("    </nav>\n  </aside>\n  <main>\n")
	b.WriteString(bodyHTML)
	b.WriteString("  </main>\n</body>\n</html>\n")
	return []byte(b.String())
}

func renderOverviewPage(g *analyzer.AppGraph, opts Options) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "<h1>%s <span class=\"badge\">%s</span></h1>\n",
		html.EscapeString(g.Module), html.EscapeString(opts.Name))
	visBadge := "<span class=\"badge\">private bucket</span>"
	if opts.Public {
		visBadge = "<span class=\"badge warn\">public bucket</span>"
	}
	fmt.Fprintf(&b, "<p>Generated project documentation bundle (level: <code>%s</code>) %s.</p>\n",
		html.EscapeString(opts.Level), visBadge)

	b.WriteString("<div class=\"cards\">\n")
	for _, s := range opts.Slices {
		stat := sliceStat(g, opts, s)
		fmt.Fprintf(&b, "  <div class=\"card\"><h3>%s</h3><div class=\"stat\">%s</div><a href=\"%s.html\">View %s &rarr;</a></div>\n",
			html.EscapeString(sliceTitle(s)),
			html.EscapeString(stat),
			html.EscapeString(s),
			html.EscapeString(sliceTitle(s)))
	}
	b.WriteString("</div>\n")

	b.WriteString("<h2>Enabled Documentation Slices</h2>\n<table>\n<thead><tr><th>Slice</th><th>Page</th><th>Summary</th></tr></thead>\n<tbody>\n")
	for _, s := range opts.Slices {
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><a href=\"%s.html\">%s</a></td><td>%s</td></tr>\n",
			html.EscapeString(s),
			html.EscapeString(s),
			html.EscapeString(sliceTitle(s)),
			html.EscapeString(sliceStat(g, opts, s)))
	}
	b.WriteString("</tbody>\n</table>\n")
	return pageShell(g, opts, "overview", "Overview", b.String())
}

func sliceStat(g *analyzer.AppGraph, opts Options, s string) string {
	switch s {
	case "architecture":
		return fmt.Sprintf("%d services", len(g.Services))
	case "apis":
		return fmt.Sprintf("%d routes", len(visibleRoutes(g, opts)))
	case "schedules":
		n := 0
		for _, h := range g.Handlers {
			if h.Kind == "cron" {
				n++
			}
		}
		return fmt.Sprintf("%d crons", n)
	case "resources":
		return fmt.Sprintf("%d resources", len(g.Resources))
	case "roles":
		return fmt.Sprintf("%d roles", len(g.Roles))
	case "footprints":
		return fmt.Sprintf("%d compute profiles", len(g.Services))
	case "permissions":
		return fmt.Sprintf("%d usage edges", len(g.Edges))
	default:
		return ""
	}
}

func renderNotFoundPage(g *analyzer.AppGraph, opts Options) []byte {
	body := "<h1>404 — Page Not Found</h1>\n<p>The requested documentation page does not exist in this bundle. <a href=\"index.html\">Return to Overview</a>.</p>\n"
	return pageShell(g, opts, "", "Not Found", body)
}

func renderArchitecturePage(g *analyzer.AppGraph, opts Options) []byte {
	var b strings.Builder
	b.WriteString("<h1>Architecture</h1>\n")
	b.WriteString("<p>Service topology, asynchronous workers, event subscribers, and data stores derived statically from package-level declarations.</p>\n")

	mermaidSrc := analyzer.Mermaid(g)
	b.WriteString("<h2>System Topology (Mermaid)</h2>\n")
	fmt.Fprintf(&b, "<pre class=\"mermaid\">%s</pre>\n", html.EscapeString(mermaidSrc))
	b.WriteString(`<script type="module">
  import mermaid from 'https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.esm.min.mjs';
  mermaid.initialize({ startOnLoad: true, theme: 'dark' });
</script>
`)

	b.WriteString("<h2>Deployable Services</h2>\n")
	b.WriteString("<table>\n<thead><tr><th>Service</th><th>Package</th><th>Kind</th><th>Domain</th><th>Handlers</th></tr></thead>\n<tbody>\n")
	for _, svc := range g.Services {
		kind := "HTTP Service"
		if svc.Job {
			kind = "Cloud Run Job"
		} else if svc.Singleton {
			kind = "Singleton Loop"
		} else if isAsyncWorkerService(g, svc) {
			kind = "Async Worker / Subscriber"
		}
		dom := svc.Domain
		if dom == "" {
			dom = "—"
		}
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td>%s</td><td><code>%s</code></td><td><code>%s</code></td></tr>\n",
			html.EscapeString(svc.Name),
			html.EscapeString(svc.Package),
			html.EscapeString(kind),
			html.EscapeString(dom),
			html.EscapeString(strings.Join(svc.Handlers, ", ")))
	}
	b.WriteString("</tbody>\n</table>\n")

	// Async topology table (workers, subscribers, jobs, singletons)
	var asyncHandlers []analyzer.Handler
	for _, h := range g.Handlers {
		if h.Kind == "worker" || h.Kind == "subscribe" || h.Kind == "job" || h.Kind == "singleton" {
			asyncHandlers = append(asyncHandlers, h)
		}
	}
	if len(asyncHandlers) > 0 {
		b.WriteString("<h2>Asynchronous Workers, Jobs &amp; Singletons</h2>\n")
		b.WriteString("<table>\n<thead><tr><th>Handler</th><th>Kind</th><th>Service</th><th>Source / Binding</th><th>Summary</th></tr></thead>\n<tbody>\n")
		for _, h := range asyncHandlers {
			src := h.Binding
			if h.BindingKind != "" {
				src = h.BindingKind + ":" + h.Binding
			}
			fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td><td>%s</td></tr>\n",
				html.EscapeString(h.Var),
				html.EscapeString(h.Kind),
				html.EscapeString(h.Service),
				html.EscapeString(src),
				html.EscapeString(analyzer.HandlerLine(h)))
		}
		b.WriteString("</tbody>\n</table>\n")
	}

	return pageShell(g, opts, "architecture", "Architecture", b.String())
}

func isAsyncWorkerService(g *analyzer.AppGraph, svc analyzer.Service) bool {
	if len(svc.Handlers) == 0 {
		return false
	}
	for _, h := range g.Handlers {
		if h.Service == svc.Name && (h.Kind == "api" || h.Kind == "page") {
			return false
		}
	}
	return true
}

func hasAuthResource(g *analyzer.AppGraph) bool {
	for _, r := range g.Resources {
		if r.Kind == "auth" {
			return true
		}
	}
	return false
}

func hasSignedBucketEdge(g *analyzer.AppGraph) bool {
	for _, e := range g.Edges {
		if e.ToKind == "bucket" && e.Verb == "sign" {
			return true
		}
	}
	return false
}

func formatSchemaFields(s *schemagen.Schema) string {
	if s == nil || len(s.Properties) == 0 {
		return "—"
	}
	reqSet := map[string]bool{}
	for _, r := range s.Required {
		reqSet[r] = true
	}
	keys := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		p := s.Properties[k]
		typ := p.Type
		if typ == "array" && p.Items != nil && p.Items.Type != "" {
			typ = "[]" + p.Items.Type
		}
		if typ == "" {
			typ = "any"
		}
		if reqSet[k] {
			parts = append(parts, fmt.Sprintf("%s: %s (required)", k, typ))
		} else {
			parts = append(parts, fmt.Sprintf("%s: %s", k, typ))
		}
	}
	return strings.Join(parts, ", ")
}

func renderAPIsPage(g *analyzer.AppGraph, tg *analyzer.TypedGraph, opts Options) []byte {
	var b strings.Builder
	b.WriteString("<h1>APIs &amp; Routes</h1>\n")
	b.WriteString("<p>HTTP JSON endpoints (<code>tako.API</code>) and HTML pages (<code>tako.Page</code>). Download <a href=\"openapi.json\"><code>openapi.json</code></a> for the OpenAPI 3.1 specification or <a href=\"llms.txt\"><code>llms.txt</code></a> for the plain-text agent reference.</p>\n")

	routes := visibleRoutes(g, opts)
	if len(routes) == 0 {
		b.WriteString("<p>No HTTP routes declared in this slice.</p>\n")
		return pageShell(g, opts, "apis", "APIs & Routes", b.String())
	}

	if hasAuthResource(g) {
		b.WriteString("<h2>Agent &amp; API Authentication (<code>POST /_tako/api/session</code>)</h2>\n")
		b.WriteString("<p>Exchange email &amp; password credentials (or an Identity Platform ID token) for a session bearer token, then pass <code>Authorization: Bearer &lt;token&gt;</code> on authenticated HTTP API or MCP requests:</p>\n")
		b.WriteString(`<pre>POST /_tako/api/session
Content-Type: application/json

{"email": "agent@example.com", "password": "..."}

# Response (200 OK):
{"uid": "...", "email": "agent@example.com", "token": "&lt;session-token&gt;"}

# Authenticated request header:
Authorization: Bearer &lt;session-token&gt;</pre>` + "\n")
	}

	var mcpRoutes []analyzer.Handler
	for _, h := range routes {
		if h.Tool != "" {
			mcpRoutes = append(mcpRoutes, h)
		}
	}
	if len(mcpRoutes) > 0 {
		b.WriteString("<h2>Model Context Protocol (MCP) Server (<code>POST /_tako/mcp</code>)</h2>\n")
		b.WriteString("<p>JSON-RPC 2.0 MCP endpoint supporting <code>initialize</code>, <code>tools/list</code>, and <code>tools/call</code>. Pass <code>Authorization: Bearer &lt;session-token&gt;</code> for authenticated tools.</p>\n")
		b.WriteString("<table>\n<thead><tr><th>Tool Name</th><th>Route</th><th>Description</th><th>Input Schema Fields</th><th>Output Schema Fields</th></tr></thead>\n<tbody>\n")
		multi := len(g.Services) > 1
		for _, h := range mcpRoutes {
			inS, outS := schemagen.HandlerSchemas(tg, h)
			fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td>%s</td><td><code>%s</code></td><td><code>%s</code></td></tr>\n",
				html.EscapeString(schemagen.ToolName(h, multi)),
				html.EscapeString(h.Route),
				html.EscapeString(h.Tool),
				html.EscapeString(formatSchemaFields(inS)),
				html.EscapeString(formatSchemaFields(outS)))
		}
		b.WriteString("</tbody>\n</table>\n")
	}

	if hasSignedBucketEdge(g) {
		b.WriteString("<h2>Direct Cloud Storage Uploads (<code>SignedPut</code>)</h2>\n")
		b.WriteString("<p>Endpoints that return a signed Cloud Storage <code>PUT</code> upload URL (via <code>bucket.SignedPut</code>) also return required upload headers from <code>PutPolicy.Headers()</code> (such as <code>x-goog-content-length-range: 0,&lt;maxBytes&gt;</code> and any exact <code>Content-Type</code>). Clients and agents must include every returned header in the <code>PUT &lt;upload_url&gt;</code> request.</p>\n")
	}

	b.WriteString("<h2>HTTP Routes</h2>\n")
	b.WriteString("<table>\n<thead><tr><th>Method &amp; Path</th><th>Kind</th><th>Handler</th><th>Service</th><th>Request / Response</th><th>Fields &amp; Options</th></tr></thead>\n<tbody>\n")
	for _, h := range routes {
		reqRes := "—"
		if h.ReqRef != "" || h.ResRef != "" {
			reqRes = fmt.Sprintf("%s -> %s", h.ReqRef, h.ResRef)
		}
		var details []string
		for _, bind := range h.Bindings {
			details = append(details, fmt.Sprintf("%s:%s (%s)", bind.Kind, bind.Name, bind.Type))
		}
		if h.HasBody {
			details = append(details, "json-body")
		}
		inS, outS := schemagen.HandlerSchemas(tg, h)
		if inFields := formatSchemaFields(inS); inFields != "—" {
			details = append(details, "in:{"+inFields+"}")
		}
		if outFields := formatSchemaFields(outS); outFields != "—" {
			details = append(details, "out:{"+outFields+"}")
		}
		if h.RequiresAuth {
			details = append(details, "sign-in")
		}
		if h.Policy != "" && (opts.HasSlice("roles") || opts.HasSlice("permissions") || !opts.HidePrivateRoutes()) {
			details = append(details, "policy:"+h.Policy)
		}
		if h.Private {
			details = append(details, "private")
		}
		if h.MaxAge > 0 {
			details = append(details, fmt.Sprintf("max-age:%ds", h.MaxAge))
		}
		if h.Idempotent {
			details = append(details, "idempotent")
		}
		if h.RateLimitRequests > 0 {
			details = append(details, fmt.Sprintf("rate-limit:%d/%ds", h.RateLimitRequests, h.RateLimitWindowSeconds))
		}
		if h.Tool != "" {
			details = append(details, "mcp-tool:"+h.Tool)
		}
		detailStr := strings.Join(details, ", ")
		if detailStr == "" {
			detailStr = "—"
		}
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><span class=\"badge\">%s</span></td><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td><td>%s</td></tr>\n",
			html.EscapeString(h.Route),
			html.EscapeString(h.Kind),
			html.EscapeString(h.Var),
			html.EscapeString(h.Service),
			html.EscapeString(reqRes),
			html.EscapeString(detailStr))
	}
	b.WriteString("</tbody>\n</table>\n")
	return pageShell(g, opts, "apis", "APIs & Routes", b.String())
}

func renderSchedulesPage(g *analyzer.AppGraph, opts Options) []byte {
	var b strings.Builder
	b.WriteString("<h1>Schedules</h1>\n")
	b.WriteString("<p>Recurring scheduled jobs declared with <code>tako.Cron</code> (provisioned on Cloud Scheduler in GCP).</p>\n")

	var crons []analyzer.Handler
	for _, h := range g.Handlers {
		if h.Kind == "cron" {
			crons = append(crons, h)
		}
	}
	if len(crons) == 0 {
		b.WriteString("<p>No scheduled crons declared in this project.</p>\n")
		return pageShell(g, opts, "schedules", "Schedules", b.String())
	}

	b.WriteString("<table>\n<thead><tr><th>Handler</th><th>Service</th><th>Schedule</th><th>Description</th><th>Timezone</th><th>Singleflight</th><th>Deadline</th></tr></thead>\n<tbody>\n")
	for _, h := range crons {
		tz := h.Timezone
		if tz == "" {
			tz = "UTC"
		}
		sf := "no"
		if h.Singleflight {
			sf = "yes (lease-guarded)"
		}
		deadline := "default"
		if h.Traffic != nil && h.Traffic.TimeoutSeconds > 0 {
			deadline = fmt.Sprintf("%ds", h.Traffic.TimeoutSeconds)
		}
		desc := h.Schedule
		if sc, err := schedule.Parse(h.Schedule); err == nil {
			desc = sc.UnixCron()
		}
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td><td>%s</td><td><code>%s</code></td><td>%s</td><td><code>%s</code></td></tr>\n",
			html.EscapeString(h.Var),
			html.EscapeString(h.Service),
			html.EscapeString(h.Schedule),
			html.EscapeString(desc),
			html.EscapeString(tz),
			html.EscapeString(sf),
			html.EscapeString(deadline))
	}
	b.WriteString("</tbody>\n</table>\n")
	return pageShell(g, opts, "schedules", "Schedules", b.String())
}

func renderResourcesPage(g *analyzer.AppGraph, opts Options) []byte {
	var b strings.Builder
	b.WriteString("<h1>Resources</h1>\n")
	b.WriteString("<p>Declared data stores, queues, topics, buckets, disks, secrets, flags, and operational primitives.</p>\n")

	if len(g.Resources) == 0 {
		b.WriteString("<p>No resources declared in this project.</p>\n")
		return pageShell(g, opts, "resources", "Resources", b.String())
	}

	b.WriteString("<table>\n<thead><tr><th>Kind</th><th>Name</th><th>Variable</th><th>Type / Schema</th><th>Details</th></tr></thead>\n<tbody>\n")
	for _, r := range g.Resources {
		typ := r.Type
		if typ == "" {
			typ = "—"
		}
		fmt.Fprintf(&b, "<tr><td><span class=\"badge\">%s</span></td><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td><td>%s</td></tr>\n",
			html.EscapeString(r.Kind),
			html.EscapeString(r.Name),
			html.EscapeString(r.Var),
			html.EscapeString(typ),
			html.EscapeString(analyzer.ResourceLine(r)))
	}
	b.WriteString("</tbody>\n</table>\n")
	return pageShell(g, opts, "resources", "Resources", b.String())
}

func renderRolesPage(g *analyzer.AppGraph, opts Options) []byte {
	var b strings.Builder
	b.WriteString("<h1>Roles &amp; Access Control</h1>\n")
	b.WriteString("<p>Declared user roles (<code>tako.Role</code>), authentication providers (<code>tako.Auth</code>), and route authorization policies (<code>tako.Policy</code> / <code>tako.RequireRole</code>).</p>\n")

	b.WriteString("<h2>Declared Roles</h2>\n")
	if len(g.Roles) == 0 {
		b.WriteString("<p>No user roles declared via <code>tako.Role</code>.</p>\n")
	} else {
		b.WriteString("<p>")
		for _, role := range g.Roles {
			fmt.Fprintf(&b, "<span class=\"badge\">%s</span> ", html.EscapeString(role))
		}
		b.WriteString("</p>\n")
	}

	b.WriteString("<h2>Route Access Matrix</h2>\n")
	b.WriteString("<table>\n<thead><tr><th>Route</th><th>Handler</th><th>Service</th><th>Access Posture</th><th>Policy</th><th>Required Roles</th></tr></thead>\n<tbody>\n")
	for _, h := range g.Handlers {
		if h.Kind != "api" && h.Kind != "page" {
			continue
		}
		posture := "Public"
		badgeClass := "ok"
		if h.Private {
			posture = "Private (Service IAM)"
			badgeClass = "warn"
		} else if h.Policy != "" {
			posture = "Policy-Gated"
			badgeClass = "warn"
		} else if h.RequiresAuth {
			posture = "Sign-In Required"
			badgeClass = ""
		}
		pol := h.Policy
		if pol == "" {
			pol = "—"
		}
		roles := strings.Join(h.PolicyRoles, ", ")
		if roles == "" {
			roles = "—"
		}
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td><td><span class=\"badge %s\">%s</span></td><td><code>%s</code></td><td><code>%s</code></td></tr>\n",
			html.EscapeString(h.Route),
			html.EscapeString(h.Var),
			html.EscapeString(h.Service),
			badgeClass,
			html.EscapeString(posture),
			html.EscapeString(pol),
			html.EscapeString(roles))
	}
	b.WriteString("</tbody>\n</table>\n")
	return pageShell(g, opts, "roles", "Roles & Access", b.String())
}

func renderFootprintsPage(g *analyzer.AppGraph, opts Options) []byte {
	var b strings.Builder
	b.WriteString("<h1>Footprints &amp; Traffic Sizing</h1>\n")
	b.WriteString("<p>Resolved Cloud Run compute footprints (CPU, Memory, Concurrency, Instance Scaling) and traffic configurations per deployable service.</p>\n")

	b.WriteString("<table>\n<thead><tr><th>Service</th><th>Profile</th><th>CPU</th><th>Memory</th><th>Concurrency</th><th>Min / Max Instances</th><th>CPU Always On</th><th>Startup Boost</th><th>Timeout</th><th>Session Affinity</th><th>HTTP/2</th></tr></thead>\n<tbody>\n")
	for _, svc := range g.Services {
		prof, fp, tr := resolveServiceSizing(svc)
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td><code>%d</code></td><td><code>%s</code></td><td><code>%d</code></td><td><code>%d / %d</code></td><td><code>%v</code></td><td><code>%v</code></td><td><code>%ds</code></td><td><code>%v</code></td><td><code>%v</code></td></tr>\n",
			html.EscapeString(svc.Name),
			html.EscapeString(prof),
			fp.CPU,
			html.EscapeString(fp.Memory),
			fp.Concurrency,
			fp.MinInstances,
			fp.MaxInstances,
			fp.CPUAlwaysOn == 1,
			fp.StartupBoost == 1,
			tr.TimeoutSeconds,
			tr.SessionAffinity == 1,
			tr.HTTP2 == 1)
	}
	b.WriteString("</tbody>\n</table>\n")
	return pageShell(g, opts, "footprints", "Footprints & Traffic", b.String())
}

func resolveServiceSizing(svc analyzer.Service) (string, analyzer.Footprint, analyzer.Traffic) {
	prof := svc.Profile
	if prof == "" {
		prof = "default"
	}
	fp := analyzer.ProfileKnobs[prof]
	if svc.Footprint != nil {
		fp = svc.Footprint.Over(fp)
	}
	tr := analyzer.TrafficKnobs[prof]
	if svc.Traffic != nil {
		tr = svc.Traffic.Over(tr)
	}
	return prof, fp, tr
}

func renderPermissionsPage(g *analyzer.AppGraph, opts Options) []byte {
	var b strings.Builder
	b.WriteString("<h1>IAM Permissions &amp; Least-Privilege Grants</h1>\n")
	b.WriteString("<p>Per-service GCP Service Accounts, derived IAM role bindings, service ingress posture, and static resource/call edges.</p>\n")

	cfg := provision.Config{
		Project:       "project",
		ProjectNumber: "000000000000",
		Region:        "us-central1",
		App:           g.Module,
		Env:           "prod",
	}
	desired, _ := provision.Desired(g, cfg, placeholderImages(g))

	var sas []provision.Object
	var grantObjs []provision.Object
	var runSvcs []provision.Object
	for _, o := range desired {
		switch o.Kind {
		case provision.KindServiceAccount:
			sas = append(sas, o)
		case provision.KindIamGrants:
			grantObjs = append(grantObjs, o)
		case provision.KindRunService:
			runSvcs = append(runSvcs, o)
		}
	}

	b.WriteString("<h2>Service Identities &amp; Ingress Posture</h2>\n")
	b.WriteString("<table>\n<thead><tr><th>Service</th><th>Service Account</th><th>Ingress Posture</th></tr></thead>\n<tbody>\n")
	publicBySvc := map[string]bool{}
	for _, rs := range runSvcs {
		pub, _ := rs.Spec["public"].(bool)
		publicBySvc[rs.Name] = pub
	}
	for _, svc := range g.Services {
		saEmail := cfg.SAEmail(svc.Name)
		posture := "Private (IAM Invoker)"
		if publicBySvc[cfg.Name(svc.Name)] {
			posture = "Public (allUsers roles/run.invoker)"
		} else if svc.Job {
			posture = "Cloud Run Job (IAM run.developer)"
		}
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td>%s</td></tr>\n",
			html.EscapeString(svc.Name),
			html.EscapeString(saEmail),
			html.EscapeString(posture))
	}
	_ = sas
	b.WriteString("</tbody>\n</table>\n")

	b.WriteString("<h2>Derived Least-Privilege IAM Grants</h2>\n")
	type grantRow struct {
		targetType string
		targetID   string
		role       string
		member     string
	}
	var rows []grantRow
	for _, gr := range grantObjs {
		tmap, _ := gr.Spec["target"].(map[string]any)
		ttype, _ := tmap["type"].(string)
		tid, _ := tmap["id"].(string)
		bs, _ := gr.Spec["bindings"].([]any)
		for _, raw := range bs {
			bm, _ := raw.(map[string]any)
			role, _ := bm["role"].(string)
			member, _ := bm["member"].(string)
			rows = append(rows, grantRow{targetType: ttype, targetID: tid, role: role, member: member})
		}
	}
	if len(rows) == 0 {
		b.WriteString("<p>No resource or cross-service IAM grants derived.</p>\n")
	} else {
		b.WriteString("<table>\n<thead><tr><th>Member (Identity)</th><th>IAM Role</th><th>Target Kind</th><th>Target Resource</th></tr></thead>\n<tbody>\n")
		for _, r := range rows {
			fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td><span class=\"badge\">%s</span></td><td><code>%s</code></td></tr>\n",
				html.EscapeString(r.member),
				html.EscapeString(r.role),
				html.EscapeString(r.targetType),
				html.EscapeString(r.targetID))
		}
		b.WriteString("</tbody>\n</table>\n")
	}

	b.WriteString("<h2>Static Resource &amp; Service Call Edges</h2>\n")
	if len(g.Edges) == 0 {
		b.WriteString("<p>No static edges recorded.</p>\n")
	} else {
		b.WriteString("<table>\n<thead><tr><th>From Handler</th><th>Kind</th><th>Verb</th><th>Target</th></tr></thead>\n<tbody>\n")
		for _, e := range g.Edges {
			target := e.To
			if e.ToKind != "" {
				target = e.ToKind + ":" + e.To
			}
			fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td></tr>\n",
				html.EscapeString(e.From),
				html.EscapeString(e.Kind),
				html.EscapeString(e.Verb),
				html.EscapeString(target))
		}
		b.WriteString("</tbody>\n</table>\n")
	}

	return pageShell(g, opts, "permissions", "IAM Permissions", b.String())
}

func renderLLMsTxt(g *analyzer.AppGraph, tg *analyzer.TypedGraph, opts Options) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — Project Documentation (%s)\n\n", g.Module, opts.Name)
	fmt.Fprintf(&b, "- Security Level: `%s`\n", opts.Level)
	fmt.Fprintf(&b, "- Enabled Slices: `%s`\n\n", strings.Join(opts.Slices, ", "))

	if opts.HasSlice("architecture") {
		b.WriteString("## Architecture\n\n")
		for _, svc := range g.Services {
			fmt.Fprintf(&b, "- Service `%s` (package `%s`, handlers: %s)\n",
				svc.Name, svc.Package, strings.Join(svc.Handlers, ", "))
		}
		b.WriteString("\n")
	}

	if opts.HasSlice("apis") {
		routes := visibleRoutes(g, opts)
		if hasAuthResource(g) {
			b.WriteString("## Agent & API Authentication (`POST /_tako/api/session`)\n\n")
			b.WriteString("- Exchange credentials for a session bearer token:\n")
			b.WriteString("  `POST /_tako/api/session` with JSON `{\"email\": \"<email>\", \"password\": \"<password>\"}` -> returns `{\"uid\": \"...\", \"email\": \"...\", \"token\": \"<session-token>\"}`\n")
			b.WriteString("- Send `Authorization: Bearer <session-token>` (or `Cookie: _session=<session-token>`) on authenticated requests.\n\n")
		}
		var mcpRoutes []analyzer.Handler
		for _, h := range routes {
			if h.Tool != "" {
				mcpRoutes = append(mcpRoutes, h)
			}
		}
		if len(mcpRoutes) > 0 {
			b.WriteString("## MCP Server (`POST /_tako/mcp`) & Tools\n\n")
			b.WriteString("- Endpoint: `POST /_tako/mcp` (JSON-RPC 2.0 `initialize`, `tools/list`, `tools/call`; pass `Authorization: Bearer <session-token>` for authenticated tools)\n")
			multi := len(g.Services) > 1
			for _, h := range mcpRoutes {
				inS, outS := schemagen.HandlerSchemas(tg, h)
				fmt.Fprintf(&b, "- Tool `%s` (`%s`): %s | input: {%s} | output: {%s}\n",
					schemagen.ToolName(h, multi), h.Route, h.Tool, formatSchemaFields(inS), formatSchemaFields(outS))
			}
			b.WriteString("\n")
		}
		if hasSignedBucketEdge(g) {
			b.WriteString("## Direct Cloud Storage Uploads (`SignedPut`)\n\n")
			b.WriteString("- When an endpoint returns a signed `PUT` URL and `upload_headers` (from `PutPolicy.Headers()`, e.g. `x-goog-content-length-range: 0,<maxBytes>` and any exact `Content-Type`), include every returned header in the `PUT <upload_url>` request to Cloud Storage.\n\n")
		}
		b.WriteString("## APIs & Routes\n\n")
		for _, h := range routes {
			inS, outS := schemagen.HandlerSchemas(tg, h)
			inStr := formatSchemaFields(inS)
			outStr := formatSchemaFields(outS)
			extra := ""
			if inStr != "—" || outStr != "—" {
				extra = fmt.Sprintf(" | request: {%s} -> response: {%s}", inStr, outStr)
			}
			fmt.Fprintf(&b, "- `%s` -> `%s` (%s, service `%s`, signIn=%v)%s\n",
				h.Route, h.Var, h.Kind, h.Service, h.RequiresAuth, extra)
		}
		b.WriteString("\n")
	}

	if opts.HasSlice("schedules") {
		b.WriteString("## Schedules\n\n")
		for _, h := range g.Handlers {
			if h.Kind == "cron" {
				desc := h.Schedule
				if sc, err := schedule.Parse(h.Schedule); err == nil {
					desc = sc.UnixCron()
				}
				fmt.Fprintf(&b, "- `%s`: `%s` (%s)\n", h.Var, h.Schedule, desc)
			}
		}
		b.WriteString("\n")
	}

	if opts.HasSlice("resources") {
		b.WriteString("## Resources\n\n")
		for _, r := range g.Resources {
			fmt.Fprintf(&b, "- %s\n", analyzer.ResourceLine(r))
		}
		b.WriteString("\n")
	}

	if opts.HasSlice("roles") {
		b.WriteString("## Roles & Access\n\n")
		if len(g.Roles) > 0 {
			fmt.Fprintf(&b, "- Declared Roles: %s\n", strings.Join(g.Roles, ", "))
		}
		for _, h := range g.Handlers {
			if (h.Kind == "api" || h.Kind == "page") && (h.RequiresAuth || h.Private || h.Policy != "") {
				fmt.Fprintf(&b, "- `%s`: signIn=%v, private=%v, policy=%q, roles=%v\n",
					h.Route, h.RequiresAuth, h.Private, h.Policy, h.PolicyRoles)
			}
		}
		b.WriteString("\n")
	}

	if opts.HasSlice("footprints") {
		b.WriteString("## Footprints\n\n")
		for _, svc := range g.Services {
			prof, fp, _ := resolveServiceSizing(svc)
			fmt.Fprintf(&b, "- `%s` (profile `%s`): CPU=%d, Memory=%s, Concurrency=%d, Min=%d, Max=%d\n",
				svc.Name, prof, fp.CPU, fp.Memory, fp.Concurrency, fp.MinInstances, fp.MaxInstances)
		}
		b.WriteString("\n")
	}

	if opts.HasSlice("permissions") {
		b.WriteString("## IAM Permissions\n\n")
		cfg := provision.Config{
			Project:       "project",
			ProjectNumber: "000000000000",
			Region:        "us-central1",
			App:           g.Module,
			Env:           "prod",
		}
		desired, _ := provision.Desired(g, cfg, placeholderImages(g))
		for _, o := range desired {
			if o.Kind == provision.KindIamGrants {
				tmap, _ := o.Spec["target"].(map[string]any)
				bs, _ := o.Spec["bindings"].([]any)
				for _, raw := range bs {
					bm, _ := raw.(map[string]any)
					fmt.Fprintf(&b, "- `%v` -> `%v` on `%v` `%v`\n",
						bm["member"], bm["role"], tmap["type"], tmap["id"])
				}
			}
		}
		b.WriteString("\n")
	}

	return []byte(b.String())
}

func placeholderImages(g *analyzer.AppGraph) map[string]string {
	m := make(map[string]string, len(g.Services))
	for _, svc := range g.Services {
		m[svc.Name] = "image:" + svc.Name
	}
	return m
}
