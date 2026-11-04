package schemagen

import (
	"fmt"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// UserNotesMarker is the HTML comment marker in AGENTS.md below which
// user-authored notes are preserved across `tako gen agents` runs.
const UserNotesMarker = "<!-- USER NOTES -->"

// GenerateAgentsMD synthesizes a live, application-specific AGENTS.md
// from the analyzed TypedGraph, preserving any section below
// <!-- USER NOTES --> in existingContent.
func GenerateAgentsMD(tg *analyzer.TypedGraph, existingContent string) string {
	g := tg.Graph
	var b strings.Builder

	fmt.Fprintf(&b, "# AGENTS.md — %s (Tako Application Context)\n\n", g.Module)
	b.WriteString("This application is built with **Tako**, a statically-analyzed framework over Google Cloud Run.\n")
	b.WriteString(" Infrastructure, routing, IAM, composite indexes, queues, and MCP tools are derived directly from package-level declarations.\n\n")

	b.WriteString("## Agent & Developer Workflow\n\n")
	b.WriteString("- `tako check --json` — Fast (<1s) static analysis, site-shadow check, version-skew check, and codegen under `.tako/gen/`. Always run after edits; diagnostics include actionable `hint` and `doc` fields.\n")
	b.WriteString("- `tako test` — Analyze, refresh `.tako/gen/` and test harness overlays, and run `go test ./...`. **Always use `tako test` instead of bare `go test`** when routes, handlers, or templates change.\n")
	b.WriteString("- `tako call <HandlerVar|Route> [json]` — Invoke any declared handler in-process against `.tako/data` (supports `--as <user>`, `--roles r1,r2`, `--flag k=v`).\n")
	b.WriteString("- `tako mcp` — Start the built-in Model Context Protocol (MCP) server over stdio for structured graph, schema, docs, test, and local data tools.\n")
	b.WriteString("- `tako docs <topic>` / `tako docs search <q>` / `tako docs explain <rule>` — Read embedded SDK documentation or explain any `tako/<rule>` diagnostic.\n")
	b.WriteString("- `tako gen agents` — Regenerate this `AGENTS.md` file from current source declarations.\n\n")

	// Services & Handlers
	fmt.Fprintf(&b, "## Architecture (%d service%s, %d handler%s, %d resource%s)\n\n",
		len(g.Services), pluralS(len(g.Services)),
		len(g.Handlers), pluralS(len(g.Handlers)),
		len(g.Resources), pluralS(len(g.Resources)))

	for _, svc := range g.Services {
		fmt.Fprintf(&b, "### Service `%s` (`%s`, profile: `%s`)\n\n", svc.Name, svc.Package, svc.Profile)
		var svcHandlers []analyzer.Handler
		for _, h := range g.Handlers {
			if h.Service == svc.Name {
				svcHandlers = append(svcHandlers, h)
			}
		}
		if len(svcHandlers) > 0 {
			b.WriteString("| Handler | Kind / Trigger | Request → Response | Guards & Features |\n")
			b.WriteString("|---|---|---|---|\n")
			for _, h := range svcHandlers {
				trigger := h.Route
				if trigger == "" && h.Schedule != "" {
					trigger = "schedule: " + h.Schedule
				} else if trigger == "" && h.Binding != "" {
					trigger = h.Kind + " ← " + h.Binding
				} else if trigger == "" {
					trigger = h.Kind
				}
				sig := "—"
				if h.ReqRef != "" || h.ResRef != "" {
					req := h.ReqRef
					if req == "" {
						req = "struct{}"
					}
					res := h.ResRef
					if res == "" {
						res = "error"
					}
					sig = fmt.Sprintf("`%s` → `%s`", req, res)
				}
				var guards []string
				if h.Policy != "" {
					if len(h.PolicyRoles) > 0 {
						guards = append(guards, fmt.Sprintf("Allow: `%s` (%s)", h.Policy, strings.Join(h.PolicyRoles, ", ")))
					} else {
						guards = append(guards, fmt.Sprintf("Allow: `%s`", h.Policy))
					}
				} else if h.RequiresAuth {
					guards = append(guards, "SignIn")
				}
				if h.Private {
					guards = append(guards, "Private (IAM)")
				}
				if h.Idempotent {
					guards = append(guards, "Idempotent")
				}
				if h.RateLimitRequests > 0 {
					guards = append(guards, fmt.Sprintf("RateLimit(%d/%ds)", h.RateLimitRequests, h.RateLimitWindowSeconds))
				}
				if h.Tool != "" {
					guards = append(guards, fmt.Sprintf("MCP Tool (`%s`)", ToolName(h, len(g.Services) > 1)))
				}
				guardStr := "public"
				if len(guards) > 0 {
					guardStr = strings.Join(guards, ", ")
				}
				fmt.Fprintf(&b, "| `%s` (%s) | `%s` | %s | %s |\n",
					h.Var, analyzer.ShortPos(h.Pos), trigger, sig, guardStr)
			}
			b.WriteString("\n")
		}
	}

	// Resources
	if len(g.Resources) > 0 {
		b.WriteString("## Declared Resources & Schemas\n\n")
		b.WriteString("| Kind | Name / Var | Type | Details (Indexes, TTL, Delivery, Providers) |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, r := range g.Resources {
			if r.Kind == "service" {
				continue
			}
			typ := r.Type
			if typ == "" {
				typ = "—"
			} else {
				typ = "`" + typ + "`"
			}
			var details []string
			if len(r.Indexes) > 0 {
				details = append(details, "indexed: `"+strings.Join(r.Indexes, "`, `")+"`")
			}
			if r.TTLField != "" {
				details = append(details, "ttl: `"+r.TTLField+"`")
			}
			if len(r.Composites) > 0 {
				var comps []string
				for _, c := range r.Composites {
					comps = append(comps, "("+strings.Join(c, ", ")+")")
				}
				details = append(details, "composites: "+strings.Join(comps, "; "))
			}
			if len(r.Providers) > 0 {
				details = append(details, "providers: "+strings.Join(r.Providers, ", "))
			}
			if r.RateLimit > 0 {
				details = append(details, fmt.Sprintf("rate: %d/s", r.RateLimit))
			}
			if r.Condition != "" {
				details = append(details, "condition: "+r.Condition)
			}
			detailStr := "—"
			if len(details) > 0 {
				detailStr = strings.Join(details, " · ")
			}
			fmt.Fprintf(&b, "| `%s` | `%s` (`%s`) | %s | %s |\n",
				r.Kind, r.Name, r.Var, typ, detailStr)
		}
		b.WriteString("\n")
	}

	if len(g.Roles) > 0 {
		fmt.Fprintf(&b, "**Declared Roles**: `%s`\n\n", strings.Join(g.Roles, "`, `"))
	}

	// Core Static Analysis Rules
	b.WriteString("## Static Analysis Rules to Follow\n\n")
	b.WriteString("1. **Package-level declarations (`tako/package-level`)**: Every handler (`API`, `Page`, `Worker`, `Subscribe`, `Cron`, `Runner`, `Singleton`) and resource (`Entity`, `Bucket`, `Queue`, `Topic`, `Cache`, `Secret`, `Lock`, `Job`, `Flag`, `Audit`, `Push`, `Site`, `Auth`, `Role`, `Policy`) must be a package-level `var`. Handler vars must be **exported**.\n")
	b.WriteString("2. **Compile-time constants (`tako/const-route`, `tako/const-name`, `tako/config-literal`)**: Routes, resource names, cron schedules, `.Where`/`OrderBy` field names, and config structs (`tako.Route{...}`) must be written as compile-time constants/literals.\n")
	b.WriteString("3. **Entity struct tags (`tako/entity-id`, `tako/unindexed-query`, `tako/entity-ttl`)**: Every `tako.Entity[T]` struct requires one `string` field tagged `entity:\"id\"`. Fields queried in `.Where` or `.OrderBy` must be tagged `entity:\"index\"` and referenced by **Go struct field name** (not JSON tag). Optional TTL expiration uses `time.Time` tagged `entity:\"ttl\"`.\n")
	b.WriteString("4. **Request binding (`tako/route-params`, `tako/body-on-get`, `tako/page-binding`)**: Exported request structs bind `{param}` route segments via `path:\"param\"`, query strings via `query:\"k\"`, and forms via `form:\"k\"`. Untagged fields decode from JSON bodies on non-GET `tako.API` routes.\n")
	b.WriteString("5. **Cross-service calls (`tako/raw-call`)**: Call other packages' endpoints through the exported `Endpoint` var (`pkg.Endpoint.Call(ctx, req)`), never by calling the underlying handler function directly.\n")
	b.WriteString("6. **App MCP Tools (`tako/tool-api`)**: Expose JSON `tako.API` endpoints as MCP tools on `POST /_tako/api/mcp` by setting `tako.Route{Tool: \"description\"}`.\n\n")

	b.WriteString(UserNotesMarker + "\n")
	if idx := strings.Index(existingContent, UserNotesMarker); idx >= 0 {
		tail := strings.TrimPrefix(existingContent[idx+len(UserNotesMarker):], "\n")
		if strings.TrimSpace(tail) != "" {
			b.WriteString(tail)
			if !strings.HasSuffix(tail, "\n") {
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
