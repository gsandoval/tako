package docserver

import (
	"html/template"
)

// One stylesheet, inlined. Works offline on an airplane with system fonts,
// with optional web fonts when online.
const style = `
@import url('https://fonts.googleapis.com/css2?family=Geist+Mono:wght@100..900&family=Give+You+Glory&family=Inter:ital,opsz,wght@0,14..32,100..900;1,14..32,100..900&display=swap');

:root {
  --bg: #fafafa;
  --surface: #ffffff;
  --surface-alt: #f5f5f5;
  --rule: #e5e5e5;
  --rule-dark: #d4d4d4;
  --ink: #0a0a0a;
  --dim: #737373;
  --tertiary: #a3a3a3;
  --code: #f6f6f4;

  /* Solid Flat Accent Colors */
  --accent-blue: #2563eb;
  --accent-blue-bg: #eff6ff;
  --accent-blue-border: #bfdbfe;

  --accent-emerald: #059669;
  --accent-emerald-bg: #ecfdf5;
  --accent-emerald-border: #a7f3d0;

  --accent-purple: #7c3aed;
  --accent-purple-bg: #f5f3ff;
  --accent-purple-border: #ddd6fe;

  --accent-amber: #d97706;
  --accent-amber-bg: #fffbeb;
  --accent-amber-border: #fde68a;

  --accent-pink: #db2777;
  --accent-pink-bg: #fdf2f8;
  --accent-pink-border: #fbcfe8;

  --accent-cyan: #0284c7;
  --accent-cyan-bg: #f0f9ff;
  --accent-cyan-border: #bae6fd;

  --accent-orange: #ea580c;
  --accent-orange-bg: #fff7ed;
  --accent-orange-border: #fed7aa;

  --font-sans: 'Inter', -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  --font-mono: 'Geist Mono', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  --font-cursive: 'Give You Glory', cursive, sans-serif;
}

* { box-sizing: border-box; }
body { margin:0; background:var(--bg); color:var(--ink); font:16px/1.6 var(--font-sans); -webkit-font-smoothing:antialiased; }

header {
  border-bottom:1px solid var(--rule); padding:.85rem 1.4rem; display:flex; gap:1.5rem;
  align-items:center; position:sticky; top:0; background:var(--surface); z-index:50;
}
header .brand { font-weight:700; letter-spacing:-.02em; font-size:1.05rem; }
header a { color:var(--dim); text-decoration:none; font-size:.92rem; font-weight:500; transition:color .15s ease; }
header a.on, header a:hover { color:var(--ink); font-weight:600; }

.layout { display:flex; align-items:flex-start; gap:2.5rem; max-width:72rem; margin:0 auto; padding:0 1.4rem; }
nav.side {
  width:15rem; flex:none; position:sticky; top:3.4rem; padding:1.6rem 0; font-size:.92rem;
  max-height:calc(100vh - 3.4rem); overflow:auto;
}
nav.side a { display:block; padding:.22rem 0; color:var(--dim); text-decoration:none; transition:color .15s; }
nav.side a:hover { color:var(--ink); }
nav.side a.on { color:var(--ink); font-weight:600; }
nav.side .group { margin-top:1.2rem; font-size:.76rem; text-transform:uppercase; letter-spacing:.06em; color:var(--tertiary); font-weight:700; }

main { min-width:0; flex:1; padding:1.6rem 0 6rem; }
h1 { font-size:1.85rem; line-height:1.25; letter-spacing:-.03em; margin:.4rem 0 1.2rem; font-weight:800; }
h2 { font-size:1.35rem; margin:2.2rem 0 .7rem; padding-bottom:.3rem; border-bottom:1px solid var(--rule); font-weight:700; }
h3 { font-size:1.1rem; margin:1.6rem 0 .5rem; font-weight:600; }
a { color:var(--accent-blue); text-decoration:none; }
a:hover { text-decoration:underline; }
p, li { max-width:46rem; }
code { background:var(--code); border-radius:4px; padding:.12rem .35rem; font-size:.88em; font-family:var(--font-mono); }
pre { background:var(--surface); border:1px solid var(--rule); border-radius:6px; padding:.85rem 1.1rem; overflow:auto; max-width:54rem; margin:1rem 0; }
pre code { background:none; padding:0; font-size:.85rem; line-height:1.55; }
blockquote { margin:1rem 0; padding:.2rem 1rem; border-left:3px solid var(--rule); color:var(--dim); }
table { border-collapse:collapse; margin:1.2rem 0; font-size:.92rem; width:100%; max-width:54rem; }
th, td { border:1px solid var(--rule); padding:.45rem .75rem; text-align:left; vertical-align:top; }
th { background:var(--surface-alt); font-weight:600; }
hr { border:0; border-top:1px solid var(--rule); margin:2rem 0; }
.repo { color:var(--dim); }
.repo code { font-size:.82em; }
.lede { color:var(--dim); font-size:1.05rem; line-height:1.6; }
.sym { list-style:none; padding:0; margin:.4rem 0 1.2rem; }
.sym li { margin:.2rem 0; }
.sym .m { padding-left:1.6rem; }
.sym code { background:none; padding:0; font-size:.86rem; }
footer { color:var(--dim); font-size:.85rem; border-top:1px solid var(--rule); padding:1.2rem 0; margin-top:3.5rem; }

/* Memo.Engineer Flat Landing Page Styles */
.memo-container { max-width:68rem; margin:0 auto; width:100%; }
.memo-section-header { font-family:var(--font-mono); font-size:.8125rem; color:var(--dim); letter-spacing:.05em; margin-bottom:.85rem; }
.memo-hero { padding:2.5rem 0 3rem; border-bottom:1px solid var(--rule); }
.hero-title-group { display:flex; flex-wrap:wrap; align-items:baseline; gap:.75rem 1rem; margin-bottom:1rem; }
.memo-hero-title { font-size:clamp(2rem, 5.5vw, 3.25rem); font-weight:800; letter-spacing:-.04em; line-height:1.15; margin:0; }
.flat-blue { color:var(--accent-blue); }
.handwritten-sticker {
  font-family:var(--font-cursive); font-size:1.45rem; color:var(--accent-orange); background:var(--accent-orange-bg);
  border:1px solid var(--accent-orange-border); padding:.15rem .65rem; border-radius:6px; transform:rotate(-3deg);
  white-space:nowrap; display:inline-block;
}
.memo-hero-desc { font-size:1.125rem; line-height:1.65; color:var(--dim); max-width:44rem; margin:0 0 1.75rem; }
.memo-hero-desc strong { color:var(--ink); }

.memo-actions { display:flex; gap:.75rem; align-items:center; flex-wrap:wrap; margin-bottom:1.75rem; }
.m-btn {
  display:inline-flex; align-items:center; gap:.5rem; padding:.75rem 1.4rem; font-size:.9rem; font-weight:500;
  border-radius:6px; text-decoration:none !important; transition:all .15s ease; cursor:pointer; border:1px solid transparent;
}
.m-btn-primary { background-color:var(--ink); color:#ffffff !important; border-color:var(--ink); }
.m-btn-primary:hover { background-color:#262626; border-color:#262626; }
.btn-dot { width:7px; height:7px; border-radius:50%; background-color:#22c55e; }
.m-btn-secondary { background-color:var(--surface); color:var(--ink) !important; border-color:var(--rule); }
.m-btn-secondary:hover { background-color:var(--surface-alt); border-color:var(--rule-dark); }
.m-btn-link { color:var(--accent-blue) !important; font-weight:600; padding:.75rem .5rem; }
.m-btn-link:hover { text-decoration:underline !important; }

.memo-install-box {
  display:flex; align-items:center; justify-content:space-between; gap:.75rem; background-color:var(--surface);
  border:1px solid var(--rule); padding:.55rem .9rem; border-radius:6px; font-family:var(--font-mono);
  font-size:.85rem; max-width:38rem; overflow:hidden;
}
.terminal-bar { display:flex; align-items:center; gap:.6rem; overflow-x:auto; white-space:nowrap; -webkit-overflow-scrolling:touch; }
.terminal-prompt { color:#16a34a; font-weight:700; flex-shrink:0; }
.memo-install-box code { background:none !important; padding:0 !important; color:var(--ink) !important; font-size:.82rem; }
.copy-tag { font-size:.72rem; color:var(--tertiary); border-left:1px solid var(--rule); padding-left:.6rem; flex-shrink:0; }

.memo-section { padding:3rem 0; border-bottom:1px solid var(--rule); }
.memo-section-title { font-size:1.65rem; font-weight:700; letter-spacing:-.03em; color:var(--ink); margin:0 0 .4rem; border:none !important; padding:0 !important; }
.memo-section-sub { color:var(--dim); font-size:.95rem; max-width:44rem; margin:0 0 1.75rem; line-height:1.55; }

.memo-project-grid { display:grid; grid-template-columns:repeat(auto-fit, minmax(18.5rem, 1fr)); gap:1.25rem; }
.memo-card {
  background-color:var(--surface); border:1px solid var(--rule); border-radius:8px; padding:1.4rem;
  text-decoration:none !important; color:inherit !important; display:flex; flex-direction:column;
  transition:border-color .15s ease;
}
.memo-card:hover { text-decoration:none !important; }
.memo-card.card-blue:hover { border-color:var(--accent-blue); }
.memo-card.card-emerald:hover { border-color:var(--accent-emerald); }
.memo-card.card-purple:hover { border-color:var(--accent-purple); }
.memo-card.card-amber:hover { border-color:var(--accent-amber); }
.memo-card.card-pink:hover { border-color:var(--accent-pink); }
.memo-card.card-cyan:hover { border-color:var(--accent-cyan); }

.card-top { display:flex; align-items:center; gap:.6rem; margin-bottom:.8rem; }
.card-id { font-family:var(--font-mono); font-size:.75rem; font-weight:600; color:var(--tertiary); }
.badge { display:inline-block; padding:.2rem .5rem; font-size:.72rem; font-family:var(--font-mono); font-weight:600; border-radius:4px; }
.badge-blue { background:var(--accent-blue-bg); color:var(--accent-blue); border:1px solid var(--accent-blue-border); }
.badge-emerald { background:var(--accent-emerald-bg); color:var(--accent-emerald); border:1px solid var(--accent-emerald-border); }
.badge-purple { background:var(--accent-purple-bg); color:var(--accent-purple); border:1px solid var(--accent-purple-border); }
.badge-amber { background:var(--accent-amber-bg); color:var(--accent-amber); border:1px solid var(--accent-amber-border); }
.badge-pink { background:var(--accent-pink-bg); color:var(--accent-pink); border:1px solid var(--accent-pink-border); }
.badge-cyan { background:var(--accent-cyan-bg); color:var(--accent-cyan); border:1px solid var(--accent-cyan-border); }

.card-status { margin-left:auto; font-family:var(--font-mono); font-size:.68rem; color:var(--tertiary); }
.card-title { margin:0 0 .45rem !important; font-size:1.2rem !important; font-weight:700; border:none !important; padding:0 !important; }
.card-title code { background:none !important; padding:0 !important; font-family:var(--font-mono); color:var(--ink); }
.card-desc { color:var(--dim); font-size:.88rem; line-height:1.55; flex:1; margin:0 0 1.2rem; }
.card-tags { display:flex; gap:.35rem; flex-wrap:wrap; margin-bottom:1rem; }
.tag { display:inline-block; padding:.18rem .45rem; font-size:.72rem; background-color:var(--surface-alt); color:var(--dim); border:1px solid var(--rule); font-family:var(--font-mono); border-radius:4px; }
.card-footer-meta { display:flex; justify-content:space-between; border-top:1px solid var(--rule); padding-top:.75rem; font-family:var(--font-mono); font-size:.72rem; color:var(--tertiary); }

.two-stories-grid { display:grid; grid-template-columns:1fr 1fr; gap:1.25rem; }
.story-panel { border-radius:8px; padding:1.6rem; text-decoration:none !important; color:inherit !important; display:flex; flex-direction:column; border:1px solid transparent; transition:border-color .15s ease; }
.local-panel { background-color:#f2fbf5; border-color:#bbf7d0; }
.local-panel:hover { border-color:var(--accent-emerald); }
.cloud-panel { background-color:#f0f7ff; border-color:#bfdbfe; }
.cloud-panel:hover { border-color:var(--accent-blue); }

.panel-header { display:flex; align-items:center; gap:.5rem; margin-bottom:.75rem; }
.pill-dot { width:8px; height:8px; border-radius:50%; }
.dot-emerald { background-color:var(--accent-emerald); }
.dot-blue { background-color:var(--accent-blue); }
.panel-tag { font-family:var(--font-mono); font-size:.75rem; font-weight:700; letter-spacing:.05em; color:var(--dim); }
.story-panel h3 { font-size:1.3rem; font-weight:700; margin:0 0 .65rem; border:none !important; padding:0 !important; color:var(--ink); }
.story-panel p { color:var(--dim); font-size:.9rem; line-height:1.6; flex:1; margin:0 0 1.25rem; }
.panel-stat-row { display:flex; gap:1.25rem; border-top:1px solid rgba(0,0,0,0.06); padding-top:.9rem; margin-bottom:1.1rem; }
.p-stat { font-family:var(--font-mono); font-size:.78rem; color:var(--dim); }
.p-stat strong { color:var(--ink); font-size:.92rem; }
.panel-link { font-size:.88rem; font-weight:600; color:var(--accent-blue); }

.tutorial-list { display:flex; flex-direction:column; gap:.65rem; }
.tutorial-item { background-color:var(--surface); border:1px solid var(--rule); border-radius:6px; padding:.9rem 1.15rem; display:flex; align-items:center; gap:1rem; text-decoration:none !important; color:inherit !important; transition:border-color .15s ease; }
.tutorial-item:hover { border-color:var(--ink); }
.t-num { font-family:var(--font-mono); font-size:.9rem; font-weight:700; color:var(--accent-blue); background:var(--accent-blue-bg); width:2rem; height:2rem; border-radius:4px; display:flex; align-items:center; justify-content:center; flex-shrink:0; }
.t-body { flex:1; min-width:0; }
.t-title { font-weight:600; font-size:.95rem; color:var(--ink); margin-bottom:.15rem; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.t-desc { font-size:.83rem; color:var(--dim); line-height:1.45; }
.t-tags { display:flex; gap:.35rem; flex-shrink:0; }

/* Mobile Responsive */
@media (max-width: 768px) {
  .layout { flex-direction:column !important; padding:0 1rem !important; gap:1.25rem !important; }
  main { padding:1rem 0 3.5rem !important; width:100% !important; }
  header { padding:.75rem 1rem !important; gap:1rem !important; overflow-x:auto !important; -webkit-overflow-scrolling:touch !important; white-space:nowrap !important; }
  header a { padding:.2rem 0; }
  .memo-hero { padding:1.75rem 0 2rem; }
  .memo-hero-title { font-size:2.15rem; line-height:1.2; }
  .hero-title-group { flex-direction:column; align-items:flex-start; gap:.5rem; }
  .handwritten-sticker { font-size:1.25rem; padding:.1rem .5rem; transform:rotate(-2deg); }
  .memo-hero-desc { font-size:1rem; line-height:1.55; margin-bottom:1.5rem; }
  .memo-actions { flex-direction:column; width:100%; gap:.65rem; }
  .m-btn { width:100%; justify-content:center; padding:.85rem 1rem; font-size:.95rem; }
  .m-btn-link { text-align:center; padding:.5rem; }
  .memo-install-box { width:100%; font-size:.78rem; }
  .terminal-bar code { font-size:.78rem; }
  .memo-project-grid { grid-template-columns:1fr; gap:1rem; }
  .two-stories-grid { grid-template-columns:1fr; gap:1rem; }
  .panel-stat-row { flex-wrap:wrap; gap:.85rem; }
  .tutorial-item { flex-direction:column; align-items:flex-start; gap:.6rem; padding:1rem; }
  .t-title { white-space:normal; }
  .t-tags { margin-top:.2rem; }

  /* Mobile scrollable chapter pill bar */
  nav.side {
    width:100% !important; position:sticky !important; top:3.2rem !important; background:var(--bg) !important;
    z-index:40 !important; display:flex !important; overflow-x:auto !important; -webkit-overflow-scrolling:touch !important;
    gap:.5rem !important; padding:.65rem 0 !important; border-bottom:1px solid var(--rule) !important;
    scrollbar-width:none !important; white-space:nowrap !important;
  }
  nav.side::-webkit-scrollbar { display:none; }
  nav.side a {
    display:inline-flex !important; align-items:center !important; padding:.35rem .8rem !important;
    background-color:var(--surface) !important; border:1px solid var(--rule) !important;
    border-radius:9999px !important; font-size:.82rem !important; color:var(--dim) !important;
    text-decoration:none !important;
  }
  nav.side a.on {
    background-color:var(--ink) !important; color:#ffffff !important; border-color:var(--ink) !important;
    font-weight:600 !important;
  }
  nav.side .group { display:none !important; }

  h1 { font-size:1.6rem !important; }
  h2 { font-size:1.25rem !important; margin:1.75rem 0 .6rem !important; }
  pre { max-width:100% !important; overflow-x:auto !important; -webkit-overflow-scrolling:touch !important; padding:.8rem !important; font-size:.82rem !important; }
  table { display:block !important; overflow-x:auto !important; -webkit-overflow-scrolling:touch !important; max-width:100% !important; font-size:.82rem !important; }
}
`

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
<div class="memo-container">
  <section class="memo-hero">
    <div class="memo-section-header">// 0x00 Framework Intro</div>
    <div class="hero-title-group">
      <h1 class="memo-hero-title">
        Write Go. <span class="flat-blue">Get a cloud.</span>
      </h1>
      <div class="handwritten-sticker">
        invisible infrastructure 🐙
      </div>
    </div>

    <p class="memo-hero-desc">
      <strong>Transparent distributed systems over Google Cloud Run.</strong> Stop repeating compute footprints, IAM permissions, and deployment configuration across YAML and Terraform: declare the architectural intent directly in your Go code, run the entire stack locally in one process, and let Tako derive the cloud automatically.
    </p>

    <div class="memo-actions">
      <a href="/guide/01-hello" class="m-btn m-btn-primary">
        <span class="btn-dot"></span> Get Started (5 min)
      </a>
      <a href="/reference/" class="m-btn m-btn-secondary">Explore Primitives</a>
      <a href="/guide/local" class="m-btn m-btn-link">The Local Story →</a>
    </div>

    <div class="memo-install-box">
      <div class="terminal-bar">
        <span class="terminal-prompt">$</span>
        <code>go install github.com/gsandoval/tako/cmd/tako@latest</code>
      </div>
      <span class="copy-tag">cli v0.1</span>
    </div>
  </section>

  <section class="memo-section">
    <div class="memo-section-header">// 0x01 Core Primitives (Declared Nouns)</div>
    <h2 class="memo-section-title">The Declared Vocabulary</h2>
    <p class="memo-section-sub">
      No comment directives, no reflection in the request path. Six interface rules reduce common cloud patterns to typed Go constructors.
    </p>

    <div class="memo-project-grid">
      <a href="/reference/api" class="memo-card card-blue">
        <div class="card-top">
          <span class="card-id">PRM-01</span>
          <span class="badge badge-blue">HTTP API</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.API</code></h3>
        <p class="card-desc">Type-safe JSON endpoints with compile-time path and query tag binding. Refuses untyped reflection in the request path.</p>
        <div class="card-tags">
          <span class="tag">Cloud Run</span>
          <span class="tag">HTTP/2</span>
          <span class="tag">Path Tags</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: In-process mux</span>
          <span>Cloud: Cloud Run service</span>
        </div>
      </a>

      <a href="/reference/entity" class="memo-card card-emerald">
        <div class="card-top">
          <span class="card-id">PRM-02</span>
          <span class="badge badge-emerald">DATASTORE</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Entity[T]</code></h3>
        <p class="card-desc">Typed document storage with atomic transactional mutations, derived secondary indexes, and zero SQL schema drift.</p>
        <div class="card-tags">
          <span class="tag">Datastore</span>
          <span class="tag">bbolt</span>
          <span class="tag">Transactions</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: .tako/data/entity.db</span>
          <span>Cloud: GCP Datastore</span>
        </div>
      </a>

      <a href="/reference/queue" class="memo-card card-purple">
        <div class="card-top">
          <span class="card-id">PRM-03</span>
          <span class="badge badge-purple">WORK QUEUE</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Queue[T]</code></h3>
        <p class="card-desc">Competing-consumer asynchronous work queue. Dispatches background jobs with automatic retry pacing and dead-lettering.</p>
        <div class="card-tags">
          <span class="tag">Cloud Tasks</span>
          <span class="tag">Channels</span>
          <span class="tag">At-Least-Once</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: Go Channels</span>
          <span>Cloud: Cloud Tasks</span>
        </div>
      </a>

      <a href="/reference/bucket" class="memo-card card-amber">
        <div class="card-top">
          <span class="card-id">PRM-04</span>
          <span class="badge badge-amber">OBJECT STORAGE</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Bucket</code></h3>
        <p class="card-desc">Blob and object storage with presigned upload and download URLs, streaming I/O, and auto-provisioned bucket permissions.</p>
        <div class="card-tags">
          <span class="tag">Cloud Storage</span>
          <span class="tag">Signed URLs</span>
          <span class="tag">Blob</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: .tako/data/buckets/</span>
          <span>Cloud: Google Cloud Storage</span>
        </div>
      </a>

      <a href="/reference/topic" class="memo-card card-pink">
        <div class="card-top">
          <span class="card-id">PRM-05</span>
          <span class="badge badge-pink">PUB/SUB</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Topic[T]</code></h3>
        <p class="card-desc">Event broadcast fan-out delivering messages to multiple independent subscriber services with OIDC authentication.</p>
        <div class="card-tags">
          <span class="tag">Pub/Sub</span>
          <span class="tag">Fan-out</span>
          <span class="tag">OIDC Tokens</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: In-process Dispatcher</span>
          <span>Cloud: GCP Pub/Sub Topic</span>
        </div>
      </a>

      <a href="/reference/site" class="memo-card card-cyan">
        <div class="card-top">
          <span class="card-id">PRM-06</span>
          <span class="badge badge-cyan">FULL-STACK WEB</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Site</code></h3>
        <p class="card-desc">Server-rendered templates (HTML + HTMX fragments) or Hugo/Astro builds embedded into the same binary — one origin, zero CORS.</p>
        <div class="card-tags">
          <span class="tag">HTMX</span>
          <span class="tag">Templates</span>
          <span class="tag">Custom Domain</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: Dev Live Reload</span>
          <span>Cloud: Cloud Run + CDN</span>
        </div>
      </a>
    </div>
  </section>

  <section class="memo-section">
    <div class="memo-section-header">// 0x02 Architectural Guarantees</div>
    <h2 class="memo-section-title">The Two Stories</h2>

    <div class="two-stories-grid">
      <a href="/guide/local" class="story-panel local-panel">
        <div class="panel-header">
          <span class="pill-dot dot-emerald"></span>
          <span class="panel-tag">THE LOCAL STORY</span>
        </div>
        <h3>Zero Emulators. Zero Docker.</h3>
        <p>
          <code>tako dev</code> runs your entire application graph inside a single native Go process. Queues and topics map to in-memory channels, so your debugger steps straight across queue and worker boundaries without network latency or container restarts.
        </p>
        <div class="panel-stat-row">
          <div class="p-stat"><strong>1</strong> process</div>
          <div class="p-stat"><strong>0</strong> emulators</div>
          <div class="p-stat"><strong>.tako/data</strong> total reset</div>
        </div>
        <span class="panel-link">Read local development guide →</span>
      </a>

      <a href="/guide/cloud" class="story-panel cloud-panel">
        <div class="panel-header">
          <span class="pill-dot dot-blue"></span>
          <span class="panel-tag">THE CLOUD STORY</span>
        </div>
        <h3>Derived Least-Privilege Cloud.</h3>
        <p>
          <code>tako deploy</code> statically inspects which handlers touch which resources, and generates classified plans with exact IAM bindings. Removing code revokes permissions automatically. No Terraform state files, no YAML manifests.
        </p>
        <div class="panel-stat-row">
          <div class="p-stat"><strong>0</strong> YAML files</div>
          <div class="p-stat"><strong>100%</strong> AST-derived IAM</div>
          <div class="p-stat"><strong>Stateless</strong> convergence</div>
        </div>
        <span class="panel-link">Read Google Cloud reconciliation →</span>
      </a>
    </div>
  </section>

  <section class="memo-section">
    <div class="memo-section-header">// 0x03 Worked Examples</div>
    <h2 class="memo-section-title">Six Progressive Tutorials</h2>
    <p class="memo-section-sub">
      Every example app is a complete tutorial chapter and an integral part of the continuous conformance test suite.
    </p>

    <div class="tutorial-list">
      <a href="/guide/01-hello" class="tutorial-item">
        <div class="t-num">01</div>
        <div class="t-body">
          <div class="t-title">hello — APIs, Request Binding, & Dev Loop</div>
          <div class="t-desc">The minimal 5-minute setup. Endpoints, path/query tags, hot reload, and your first Cloud Run deployment.</div>
        </div>
        <div class="t-tags"><span class="tag">Endpoints</span><span class="tag">Binding</span></div>
      </a>

      <a href="/guide/02-shortlink" class="tutorial-item">
        <div class="t-num">02</div>
        <div class="t-body">
          <div class="t-title">shortlink — Entities, Transactions, & Queues</div>
          <div class="t-desc">Transactional counters, background workers, and verifying exactly-once state effects under chaos testing.</div>
        </div>
        <div class="t-tags"><span class="tag">Datastore</span><span class="tag">Cloud Tasks</span></div>
      </a>

      <a href="/guide/03-gallery" class="tutorial-item">
        <div class="t-num">03</div>
        <div class="t-body">
          <div class="t-title">gallery — Buckets, Event Fan-out, & Crons</div>
          <div class="t-desc">GCS image uploads via signed URLs, Pub/Sub event fan-out, dead-letter recovery, and scheduled locks.</div>
        </div>
        <div class="t-tags"><span class="tag">GCS</span><span class="tag">Pub/Sub</span><span class="tag">Cron</span></div>
      </a>

      <a href="/guide/04-journal" class="tutorial-item">
        <div class="t-num">04</div>
        <div class="t-body">
          <div class="t-title">journal — Server-Rendered Site, HTMX, & Google Auth</div>
          <div class="t-desc">Full web application: HTML templates, CSRF security, sanitized markdown rendering, and Google Sign-In.</div>
        </div>
        <div class="t-tags"><span class="tag">HTMX</span><span class="tag">Auth</span></div>
      </a>

      <a href="/guide/05-shop" class="tutorial-item">
        <div class="t-num">05</div>
        <div class="t-body">
          <div class="t-title">shop — Multi-Service RPC, TypeScript, & Jobs</div>
          <div class="t-desc">Multiple private Cloud Run services, typed cross-service calls, generated TypeScript clients, and batch compute jobs.</div>
        </div>
        <div class="t-tags"><span class="tag">RPC</span><span class="tag">Jobs</span></div>
      </a>

      <a href="/guide/06-blog" class="tutorial-item">
        <div class="t-num">06</div>
        <div class="t-body">
          <div class="t-title">blog — Hugo Jamstack with Dynamic Islands</div>
          <div class="t-desc">Hugo builds the static documentation/blog shell while Tako handles dynamic comments and fragment endpoints.</div>
        </div>
        <div class="t-tags"><span class="tag">Hugo</span><span class="tag">Jamstack</span></div>
      </a>
    </div>
  </section>
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
