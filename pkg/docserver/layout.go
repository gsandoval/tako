package docserver

import (
	"html/template"
)

// One stylesheet, inlined; no JavaScript, no external fonts, no CDN.
// `tako docs` has to work on a plane, which rules out anything the page
// would have to fetch — and a docs server that phones out is a docs
// server that leaks what you were reading.
const style = `
:root { --ink:#1c1c1c; --dim:#666; --rule:#e3e3e3; --accent:#0b5; --bg:#fff; --code:#f6f6f4; }
* { box-sizing: border-box; }
body { margin:0; background:var(--bg); color:var(--ink);
  font:16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
header { border-bottom:1px solid var(--rule); padding:.7rem 1.2rem; display:flex; gap:1.4rem;
  align-items:baseline; position:sticky; top:0; background:var(--bg); z-index:2; }
header .brand { font-weight:700; letter-spacing:-.02em; }
header a { color:var(--dim); text-decoration:none; }
header a.on, header a:hover { color:var(--ink); }
.layout { display:flex; align-items:flex-start; gap:2.5rem; max-width:70rem; margin:0 auto; padding:0 1.2rem; }
nav.side { width:15rem; flex:none; position:sticky; top:3.2rem; padding:1.6rem 0; font-size:.92rem;
  max-height:calc(100vh - 3.2rem); overflow:auto; }
nav.side a { display:block; padding:.16rem 0; color:var(--dim); text-decoration:none; }
nav.side a:hover { color:var(--ink); }
nav.side a.on { color:var(--ink); font-weight:600; }
nav.side .group { margin-top:1rem; font-size:.78rem; text-transform:uppercase; letter-spacing:.06em; color:#999; }
main { min-width:0; flex:1; padding:1.6rem 0 6rem; }
h1 { font-size:1.7rem; line-height:1.25; letter-spacing:-.02em; margin:.4rem 0 1.2rem; }
h2 { font-size:1.25rem; margin:2.2rem 0 .7rem; padding-bottom:.2rem; border-bottom:1px solid var(--rule); }
h3 { font-size:1.05rem; margin:1.6rem 0 .5rem; }
a { color:#06c; }
p, li { max-width:44rem; }
code { background:var(--code); border-radius:3px; padding:.1rem .3rem; font-size:.88em;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
pre { background:var(--code); border:1px solid var(--rule); border-radius:5px; padding:.8rem 1rem;
  overflow:auto; max-width:52rem; }
pre code { background:none; padding:0; font-size:.85rem; line-height:1.5; }
blockquote { margin:1rem 0; padding:.2rem 1rem; border-left:3px solid var(--rule); color:var(--dim); }
table { border-collapse:collapse; margin:1rem 0; font-size:.92rem; }
th, td { border:1px solid var(--rule); padding:.35rem .6rem; text-align:left; vertical-align:top; }
hr { border:0; border-top:1px solid var(--rule); margin:2rem 0; }
.repo { color:var(--dim); }
.repo code { font-size:.82em; }
.lede { color:var(--dim); }
.sym { list-style:none; padding:0; margin:.4rem 0 1.2rem; }
.sym li { margin:.15rem 0; }
.sym .m { padding-left:1.6rem; }
.sym code { background:none; padding:0; font-size:.86rem; }
.cards { display:grid; gap:1rem; grid-template-columns:repeat(auto-fit,minmax(15rem,1fr)); max-width:52rem; }
.card { border:1px solid var(--rule); border-radius:6px; padding:1rem 1.1rem; }
.card h3 { margin:0 0 .3rem; }
.card p { margin:0; color:var(--dim); font-size:.92rem; }
footer { color:var(--dim); font-size:.85rem; border-top:1px solid var(--rule); padding:1rem 0; margin-top:3rem; }
`

// The shell is deliberately one file: a docs site whose layout needs a
// build step would be a worse joke than a docs site with no CSS.
var tmpl = template.Must(template.New("docs").Parse(`
{{define "head"}}<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{if .Title}}{{.Title}} · {{end}}tako docs</title>
<style>{{.Style}}</style></head><body>
<header>
  <span class="brand"><a href="/" style="color:inherit">tako docs</a></span>
  <a href="/guide/"{{if eq .Section "guide"}} class="on"{{end}}>guide</a>
  <a href="/reference/"{{if eq .Section "reference"}} class="on"{{end}}>reference</a>
  <a href="/api"{{if eq .Section "api"}} class="on"{{end}}>api</a>
</header>
<div class="layout">
<nav class="side">{{range .Nav}}{{if .Group}}<div class="group">{{.Title}}</div>{{else}}<a href="{{.Route}}"{{if .On}} class="on"{{end}}>{{.Title}}</a>{{end}}{{end}}</nav>
<main>{{end}}

{{define "foot"}}<footer>{{.Footer}}</footer></main></div></body></html>{{end}}

{{define "page"}}{{template "head" .}}{{.Body}}{{template "foot" .}}{{end}}

{{define "home"}}{{template "head" .}}
<h1>tako docs</h1>
<p class="lede">The documentation that shipped with this binary. Everything here
is served from memory — nothing is fetched, and nothing leaves this machine.</p>
<div class="cards">
  <div class="card"><h3><a href="/guide/">Guide</a></h3>
  <p>The six examples, in the order the framework grows, plus the local and cloud stories.</p></div>
  <div class="card"><h3><a href="/reference/">Reference</a></h3>
  <p>One page per feature, every page the same shape: declare · use · local · cloud · test · errors · not yet.</p></div>
  <div class="card"><h3><a href="/api">API</a></h3>
  <p>Every exported symbol in the tako package, with the reference page that explains it.</p></div>
</div>
{{template "foot" .}}{{end}}

{{define "api"}}{{template "head" .}}
<h1>API</h1>
<p class="lede">Every exported symbol of <code>github.com/gsandoval/tako</code>, from the
same lock file the API-freeze test compares against — so this page cannot quietly
lose one. Everything under <code>pkg/…</code> is tako's internals and is not here.</p>
{{range .Groups}}<h2 id="{{.Anchor}}">{{.Section}}</h2>
{{range .Pages}}<h3>{{if .Route}}<a href="{{.Route}}">{{.Title}}</a>{{else}}{{.Title}}{{end}}</h3>
<ul class="sym">{{range .Symbols}}<li id="sym-{{.Name}}"><code>{{.Decl}}</code></li>
{{range .Methods}}<li class="m"><code>{{.Decl}}</code></li>
{{end}}{{end}}</ul>
{{end}}{{end}}
{{template "foot" .}}{{end}}

{{define "notfound"}}{{template "head" .}}
<h1>No page here</h1>
<p>The docs have three sections: <a href="/guide/">guide</a>,
<a href="/reference/">reference</a> and <a href="/api">api</a>.</p>
{{template "foot" .}}{{end}}
`))

type navItem struct {
	Title string
	Route string
	On    bool
	Group bool // a heading in the nav, not a link
}

type view struct {
	Title   string
	Section string
	Nav     []navItem
	Body    template.HTML
	Groups  []apiGroup
	Style   template.CSS
	Footer  string
}

// apiGroup is Group with the anchor the template needs.
type apiGroup struct {
	Group
	Anchor string
}
