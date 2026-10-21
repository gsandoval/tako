package docserver

import (
	"fmt"
	"strings"
)

// List formats a concise index of every embedded guide chapter,
// reference page, and the SDK surface for `tako docs list`.
func (s *Site) List() string {
	var b strings.Builder
	b.WriteString("tako docs — embedded documentation topics\n\n")
	b.WriteString("Guide (tako docs <topic>):\n")
	for _, p := range s.Guide {
		topic := "guide"
		if p.Name != "" {
			topic = p.Name
		}
		fmt.Fprintf(&b, "  %-24s %s\n", topic, p.Title)
	}
	b.WriteString("\nReference (tako docs <topic>):\n")
	for _, p := range s.Reference {
		topic := "reference"
		if p.Name != "" {
			topic = p.Name
		}
		fmt.Fprintf(&b, "  %-24s %s\n", topic, p.Title)
	}
	b.WriteString("\nSDK Surface & Agent Bundles:\n")
	fmt.Fprintf(&b, "  %-24s %s\n", "api", "Every exported symbol and signature (surface.txt)")
	fmt.Fprintf(&b, "  %-24s %s\n", "llms.txt", "Compact index of all documentation pages for LLMs/agents")
	fmt.Fprintf(&b, "  %-24s %s\n", "llms-full.txt", "Complete concatenated guide, reference, and API surface")
	return b.String()
}

// Topic resolves a topic name (e.g. "entity", "reference/entity",
// "01-hello", "guide", "api", "llms.txt") to its raw markdown or text.
func (s *Site) Topic(query string) (string, bool) {
	q := strings.TrimSpace(strings.Trim(query, "/"))
	q = strings.TrimSuffix(q, ".md")
	switch strings.ToLower(q) {
	case "api", "surface", "api/surface", "api/surface.txt":
		return s.Surface, true
	case "llms", "llms.txt":
		return s.LLMsTxt(), true
	case "llms-full", "llms-full.txt":
		return s.LLMsFullTxt(), true
	case "guide", "guide/readme":
		if p := s.Page("/guide/"); p != nil {
			return p.Markdown, true
		}
	case "reference", "reference/readme":
		if p := s.Page("/reference/"); p != nil {
			return p.Markdown, true
		}
	}

	if p := s.Page("/" + q); p != nil {
		return p.Markdown, true
	}
	if p := s.Page("/reference/" + q); p != nil {
		return p.Markdown, true
	}
	if p := s.Page("/guide/" + q); p != nil {
		return p.Markdown, true
	}
	// Prefix match on guide numbered chapters (e.g. "01" or "hello" -> "01-hello").
	lower := strings.ToLower(q)
	for _, p := range append(append([]*Page{}, s.Reference...), s.Guide...) {
		name := strings.ToLower(p.Name)
		if name == lower || strings.HasPrefix(name, lower+"-") || strings.HasSuffix(name, "-"+lower) {
			return p.Markdown, true
		}
	}
	return "", false
}

// Search searches all embedded pages and the SDK surface for query
// (case-insensitive) and returns a human/agent-readable summary of matches.
func (s *Site) Search(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return s.List()
	}
	lower := strings.ToLower(q)
	var b strings.Builder
	matches := 0

	allPages := append(append([]*Page{}, s.Guide...), s.Reference...)
	for _, p := range allPages {
		topic := p.Name
		if topic == "" {
			topic = p.Section
		}
		var hits []string
		for lineNo, line := range strings.Split(p.Markdown, "\n") {
			if strings.Contains(strings.ToLower(line), lower) {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" {
					hits = append(hits, fmt.Sprintf("    L%d: %s", lineNo+1, trimmed))
				}
				if len(hits) >= 4 {
					break
				}
			}
		}
		if len(hits) > 0 {
			matches += len(hits)
			fmt.Fprintf(&b, "%s (%s) — read with: tako docs %s\n", topic, p.Title, topic)
			for _, h := range hits {
				b.WriteString(h + "\n")
			}
			b.WriteString("\n")
		}
	}

	var apiHits []string
	for _, line := range strings.Split(s.Surface, "\n") {
		if strings.Contains(strings.ToLower(line), lower) {
			apiHits = append(apiHits, "    "+strings.TrimSpace(line))
			if len(apiHits) >= 6 {
				break
			}
		}
	}
	if len(apiHits) > 0 {
		matches += len(apiHits)
		b.WriteString("api (SDK surface) — read with: tako docs api\n")
		for _, h := range apiHits {
			b.WriteString(h + "\n")
		}
		b.WriteString("\n")
	}

	if matches == 0 {
		return fmt.Sprintf("No matches for %q. Run `tako docs list` to see all topics.\n", query)
	}
	return b.String()
}

// LLMsTxt renders a compact /llms.txt index of all documentation pages.
func (s *Site) LLMsTxt() string {
	var b strings.Builder
	b.WriteString("# Tako\n\n")
	b.WriteString("> Tako is a statically-analyzed application framework over Google Cloud Run (with Go and CFire SDKs) that derives services, IAM, queues, pub/sub topics, cron schedules, composite indexes, and monitoring alerts directly from package-level code declarations.\n\n")
	b.WriteString("## CLI Lookup\n\n")
	b.WriteString("- `tako docs list` — list all embedded documentation topics\n")
	b.WriteString("- `tako docs <topic>` — print any guide or reference page as Markdown to stdout\n")
	b.WriteString("- `tako docs search <query>` — search across guide, reference, and SDK surface\n")
	b.WriteString("- `tako check [--json]` — run static analysis, site-shadow checks, and codegen in <1s\n\n")
	b.WriteString("## Guide\n\n")
	for _, p := range s.Guide {
		topic := p.Name
		if topic == "" {
			topic = "guide"
		}
		fmt.Fprintf(&b, "- [%s](%s): `tako docs %s`\n", p.Title, p.Route, topic)
	}
	b.WriteString("\n## Reference\n\n")
	for _, p := range s.Reference {
		topic := p.Name
		if topic == "" {
			topic = "reference"
		}
		fmt.Fprintf(&b, "- [%s](%s): `tako docs %s`\n", p.Title, p.Route, topic)
	}
	b.WriteString("\n## API Surface\n\n")
	b.WriteString("- [Exported SDK Surface](/api): `tako docs api`\n")
	b.WriteString("- [Full Documentation Bundle](/llms-full.txt): `tako docs llms-full.txt`\n")
	return b.String()
}

// LLMsFullTxt concatenates the entire guide, reference, and SDK surface into one text document.
func (s *Site) LLMsFullTxt() string {
	var b strings.Builder
	b.WriteString(s.LLMsTxt())
	b.WriteString("\n---\n\n")
	for _, p := range s.Guide {
		fmt.Fprintf(&b, "<!-- FILE: %s -->\n%s\n\n---\n\n", p.File, strings.TrimSpace(p.Markdown))
	}
	for _, p := range s.Reference {
		fmt.Fprintf(&b, "<!-- FILE: %s -->\n%s\n\n---\n\n", p.File, strings.TrimSpace(p.Markdown))
	}
	b.WriteString("<!-- FILE: api/surface.txt -->\n# Exported SDK Surface\n\n```go\n")
	b.WriteString(strings.TrimSpace(s.Surface))
	b.WriteString("\n```\n")
	return b.String()
}
