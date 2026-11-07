package docserver

import (
	"html/template"
)

// One stylesheet, inlined. Works offline on an airplane with system fonts,
// with optional web fonts when online. Supports both Technical Paper Light
// Mode (:root / [data-theme="light"]) and Obsidian Dark Mode ([data-theme="dark"]).
const style = `
@import url('https://fonts.googleapis.com/css2?family=Geist+Mono:wght@100..900&family=Inter:ital,opsz,wght@0,14..32,100..900;1,14..32,100..900&family=JetBrains+Mono:ital,wght@0,100..800;1,100..800&display=swap');

:root, [data-theme="light"] {
  --bg: #fafafa;
  --surface: #ffffff;
  --surface-alt: #f4f4f5;
  --surface-hover: #f4f4f5;
  --rule: #e4e4e7;
  --rule-dark: #d4d4d8;
  --ink: #09090b;
  --dim: #52525b;
  --tertiary: #71717a;
  --code: #f8fafc;
  --code-header: #f4f4f5;

  /* Primary Brand: Tako Hanko Vermilion (朱色) */
  --primary: #e11d48;
  --primary-hover: #be123c;
  --primary-bg: #fff1f2;
  --primary-border: #fecdd3;

  /* Solid Flat Semantic Accents */
  --accent-blue: #2563eb;
  --accent-blue-bg: #eff6ff;
  --accent-blue-border: #bfdbfe;

  --accent-emerald: #059669;
  --accent-emerald-bg: #f0fdf4;
  --accent-emerald-border: #bbf7d0;

  --accent-purple: #7c3aed;
  --accent-purple-bg: #f5f3ff;
  --accent-purple-border: #ddd6fe;

  --accent-amber: #d97706;
  --accent-amber-bg: #fffbeb;
  --accent-amber-border: #fde68a;

  --accent-pink: #e11d48;
  --accent-pink-bg: #fff1f2;
  --accent-pink-border: #fecdd3;

  --accent-cyan: #0284c7;
  --accent-cyan-bg: #f0f9ff;
  --accent-cyan-border: #bae6fd;

  --accent-orange: #ea580c;
  --accent-orange-bg: #fff7ed;
  --accent-orange-border: #fed7aa;

  --story-local-bg: #f0fdf4;
  --story-local-border: #bbf7d0;
  --story-cloud-bg: #eff6ff;
  --story-cloud-border: #bfdbfe;

  --font-sans: 'Inter', -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  --font-mono: 'JetBrains Mono', 'Geist Mono', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  --font-serif-jp: "Hiragino Mincho ProN", "YuMincho", "Noto Serif JP", serif;
}

[data-theme="dark"] {
  --bg: #0d1117;
  --surface: #161b22;
  --surface-alt: #1c2128;
  --surface-hover: #21262d;
  --rule: #30363d;
  --rule-dark: #484f58;
  --ink: #f0f6fc;
  --dim: #8b949e;
  --tertiary: #6e7681;
  --code: #090d12;
  --code-header: #161b22;

  /* Primary Brand: Tako Vermilion (Dark Luminance) */
  --primary: #f43f5e;
  --primary-hover: #e11d48;
  --primary-bg: rgba(244, 63, 94, 0.12);
  --primary-border: rgba(244, 63, 94, 0.4);

  --accent-blue: #38bdf8;
  --accent-blue-bg: rgba(56, 189, 248, 0.1);
  --accent-blue-border: rgba(56, 189, 248, 0.35);

  --accent-emerald: #22c55e;
  --accent-emerald-bg: rgba(34, 197, 94, 0.1);
  --accent-emerald-border: rgba(34, 197, 94, 0.35);

  --accent-purple: #a855f7;
  --accent-purple-bg: rgba(168, 85, 247, 0.12);
  --accent-purple-border: rgba(168, 85, 247, 0.35);

  --accent-amber: #f59e0b;
  --accent-amber-bg: rgba(245, 158, 11, 0.12);
  --accent-amber-border: rgba(245, 158, 11, 0.35);

  --accent-pink: #f43f5e;
  --accent-pink-bg: rgba(244, 63, 94, 0.12);
  --accent-pink-border: rgba(244, 63, 94, 0.35);

  --accent-cyan: #38bdf8;
  --accent-cyan-bg: rgba(56, 189, 248, 0.1);
  --accent-cyan-border: rgba(56, 189, 248, 0.35);

  --accent-orange: #fb923c;
  --accent-orange-bg: rgba(251, 146, 60, 0.12);
  --accent-orange-border: rgba(251, 146, 60, 0.35);

  --story-local-bg: #111d18;
  --story-local-border: rgba(34, 197, 94, 0.35);
  --story-cloud-bg: #101926;
  --story-cloud-border: rgba(56, 189, 248, 0.35);
}

* { box-sizing: border-box; }
::selection { background: var(--primary); color: #ffffff; }
body {
  margin: 0;
  background: var(--bg);
  color: var(--ink);
  font: 15px/1.65 var(--font-sans);
  -webkit-font-smoothing: antialiased;
}

/* Sticky Top Navigation Bar */
header.top-nav {
  border-bottom: 1px solid var(--rule);
  padding: 0 1.5rem;
  height: 3.5rem;
  display: flex;
  gap: 1.25rem;
  align-items: center;
  position: sticky;
  top: 0;
  background: var(--bg);
  z-index: 60;
}
header .brand-link {
  display: inline-flex;
  align-items: center;
  gap: 0.55rem;
  text-decoration: none !important;
  color: var(--ink) !important;
  font-weight: 800;
  font-family: var(--font-mono);
  font-size: 1.08rem;
  letter-spacing: -0.03em;
  flex-shrink: 0;
}
header .brand-kamon {
  width: 28px;
  height: 28px;
  flex-shrink: 0;
  display: block;
}
header .hanko-stamp {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-family: var(--font-serif-jp);
  font-size: 0.72rem;
  font-weight: 700;
  line-height: 1;
  color: var(--primary);
  background: var(--primary-bg);
  border: 1px solid var(--primary-border);
  padding: 0.18rem 0.32rem;
  border-radius: 3px;
}
header .brand-docs-sub {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  font-weight: 600;
  color: var(--tertiary);
  border-left: 1px solid var(--rule);
  padding-left: 0.55rem;
}
header .nav-links {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  height: 100%;
  margin-left: 0.25rem;
}
header .nav-links a {
  color: var(--dim);
  text-decoration: none;
  font-family: var(--font-mono);
  font-size: 0.82rem;
  font-weight: 500;
  padding: 0 0.7rem;
  height: 100%;
  display: inline-flex;
  align-items: center;
  border-bottom: 2px solid transparent;
  transition: color 0.12s ease, border-color 0.12s ease;
}
header .nav-links a:hover {
  color: var(--ink);
  text-decoration: none;
}
header .nav-links a.on {
  color: var(--ink);
  font-weight: 700;
  border-bottom-color: var(--primary);
}
header .header-right {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 0.55rem;
}
header .github-link {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  color: var(--dim);
  font-size: 0.78rem;
  font-weight: 600;
  text-decoration: none !important;
  padding: 0.28rem 0.6rem;
  border-radius: 4px;
  border: 1px solid var(--rule);
  background: var(--surface);
  transition: all 0.12s ease;
  font-family: var(--font-mono);
}
header .github-link:hover {
  color: var(--ink);
  border-color: var(--rule-dark);
  background: var(--surface-hover);
}
header .github-link svg { flex-shrink: 0; }

/* Segmented Controls (Go / cfire & Light / Dark) */
.code-tabs-switch, .header-lang-switch, .header-theme-switch {
  display: inline-flex;
  align-items: center;
  background: var(--surface-alt);
  border: 1px solid var(--rule);
  padding: 2px;
  border-radius: 4px;
  gap: 2px;
}
.code-tab-btn, .header-lang-btn, .header-theme-btn {
  border: 0;
  background: transparent;
  color: var(--dim);
  font: 600 0.73rem/1 var(--font-mono);
  padding: 0.28rem 0.55rem;
  border-radius: 3px;
  cursor: pointer;
  transition: all 0.12s ease;
  display: inline-flex;
  align-items: center;
  gap: 0.28rem;
}
.code-tab-btn:hover, .header-lang-btn:hover, .header-theme-btn:hover {
  color: var(--ink);
}
.code-tab-btn.on, .header-lang-btn.on, .header-theme-btn.on {
  background: var(--surface);
  color: var(--ink);
  box-shadow: 0 0 0 1px var(--rule);
}
.header-lang-btn.on {
  color: var(--primary);
}

/* Unified Go / cfire Code Block Switch */
.code-tabs {
  max-width: 54rem;
  margin: 1.1rem 0;
  border: 1px solid var(--rule);
  border-radius: 6px;
  background: var(--code);
  overflow: hidden;
}
.code-tabs-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.35rem 0.7rem;
  background: var(--code-header);
  border-bottom: 1px solid var(--rule);
  flex-wrap: wrap;
}
.code-tabs-meta {
  font-family: var(--font-mono);
  font-size: 0.76rem;
  color: var(--dim);
  margin-left: auto;
}
.code-tab-caption { display: none; }
.code-tab-caption.on { display: inline; }
.code-tab-pane { display: none; }
.code-tab-pane.on { display: block; }
.code-tab-pane pre { margin: 0; border: 0; border-radius: 0; max-width: none; background: var(--code); }

.code-copy-btn {
  border: 1px solid var(--rule);
  background: var(--surface);
  color: var(--dim);
  font: 600 0.7rem/1 var(--font-mono);
  padding: 0.24rem 0.5rem;
  border-radius: 3px;
  cursor: pointer;
  transition: all 0.12s ease;
}
.code-copy-btn:hover {
  color: var(--ink);
  border-color: var(--rule-dark);
}
.code-copy-btn.copied {
  color: var(--accent-emerald);
  border-color: var(--accent-emerald-border);
}

/* 3-Column Documentation Chassis */
.layout {
  display: flex;
  align-items: flex-start;
  gap: 2.25rem;
  max-width: 88rem;
  margin: 0 auto;
  padding: 0 1.5rem;
}
.layout.layout-home {
  max-width: 78rem;
}
.layout.layout-home nav.side,
.layout.layout-home aside.toc {
  display: none;
}

nav.side {
  width: 15.5rem;
  flex: none;
  position: sticky;
  top: 3.5rem;
  padding: 1.5rem 0.75rem 2.5rem 0;
  font-size: 0.86rem;
  max-height: calc(100vh - 3.5rem);
  overflow-y: auto;
  border-right: 1px solid var(--rule);
}
nav.side a {
  display: block;
  padding: 0.28rem 0.65rem;
  margin: 1px 0;
  color: var(--dim);
  text-decoration: none;
  font-family: var(--font-mono);
  font-size: 0.81rem;
  border-left: 2px solid transparent;
  border-radius: 0 4px 4px 0;
  transition: all 0.12s ease;
}
nav.side a:hover {
  color: var(--ink);
  background: var(--surface-hover);
  text-decoration: none;
}
nav.side a.on {
  color: var(--primary);
  background: var(--primary-bg);
  border-left-color: var(--primary);
  font-weight: 700;
}
nav.side .group {
  margin: 1.15rem 0 0.35rem;
  padding: 0 0.65rem;
  font-family: var(--font-mono);
  font-size: 0.68rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--tertiary);
  font-weight: 700;
}
nav.side .group:first-child {
  margin-top: 0.2rem;
}

main {
  min-width: 0;
  flex: 1;
  padding: 1.75rem 0 5rem;
  max-width: 52rem;
}
.layout.layout-home main {
  max-width: none;
  padding-top: 0;
}

/* Right-Rail "On This Page" TOC */
aside.toc {
  width: 14rem;
  flex: none;
  position: sticky;
  top: 3.5rem;
  padding: 1.75rem 0 2rem 1.25rem;
  max-height: calc(100vh - 3.5rem);
  overflow-y: auto;
  border-left: 1px solid var(--rule);
  font-size: 0.8rem;
}
aside.toc .toc-header {
  font-family: var(--font-mono);
  font-size: 0.68rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--tertiary);
  margin-bottom: 0.6rem;
}
aside.toc .toc-list {
  list-style: none;
  padding: 0;
  margin: 0 0 1.5rem;
}
aside.toc .toc-list li {
  margin: 0.15rem 0;
}
aside.toc .toc-list a {
  display: block;
  padding: 0.2rem 0 0.2rem 0.65rem;
  color: var(--dim);
  text-decoration: none;
  border-left: 2px solid transparent;
  font-size: 0.78rem;
  line-height: 1.4;
  transition: all 0.12s ease;
}
aside.toc .toc-list a.toc-h3 {
  padding-left: 1.25rem;
  font-size: 0.74rem;
}
aside.toc .toc-list a:hover {
  color: var(--ink);
  text-decoration: none;
}
aside.toc .toc-list a.active {
  color: var(--primary);
  border-left-color: var(--primary);
  font-weight: 600;
}
aside.toc .toc-tools {
  border-top: 1px solid var(--rule);
  padding-top: 0.85rem;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
aside.toc .toc-tool-link {
  font-family: var(--font-mono);
  font-size: 0.73rem;
  color: var(--dim);
  text-decoration: none;
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
}
aside.toc .toc-tool-link:hover {
  color: var(--ink);
  text-decoration: none;
}

/* Breadcrumb & CLI Topic Bar on Doc Pages */
.doc-meta-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  flex-wrap: wrap;
  margin-bottom: 0.85rem;
  padding-bottom: 0.65rem;
  border-bottom: 1px solid var(--rule);
}
.doc-breadcrumb {
  font-family: var(--font-mono);
  font-size: 0.74rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--tertiary);
}
.doc-breadcrumb span { color: var(--primary); }
.doc-cli-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  background: var(--code);
  border: 1px solid var(--rule);
  border-radius: 4px;
  padding: 0.2rem 0.55rem;
  font-family: var(--font-mono);
  font-size: 0.74rem;
  color: var(--dim);
  cursor: pointer;
}
.doc-cli-pill:hover {
  border-color: var(--rule-dark);
  color: var(--ink);
}
.doc-cli-pill .prompt {
  color: var(--accent-emerald);
  font-weight: 700;
}

/* Typography & Prose */
h1 {
  font-size: 1.9rem;
  line-height: 1.22;
  letter-spacing: -0.03em;
  margin: 0.2rem 0 1.15rem;
  font-weight: 700;
}
h2 {
  font-size: 1.32rem;
  letter-spacing: -0.02em;
  margin: 2.25rem 0 0.75rem;
  padding-bottom: 0.35rem;
  border-bottom: 1px solid var(--rule);
  font-weight: 650;
}
h3 {
  font-size: 1.06rem;
  letter-spacing: -0.01em;
  margin: 1.6rem 0 0.5rem;
  font-weight: 600;
}
a {
  color: var(--accent-blue);
  text-decoration: none;
}
a:hover {
  text-decoration: underline;
}
p, li {
  max-width: 48rem;
}
code {
  background: var(--surface-alt);
  border: 1px solid var(--rule);
  border-radius: 3px;
  padding: 0.1rem 0.32rem;
  font-size: 0.85em;
  font-family: var(--font-mono);
}
pre {
  background: var(--code);
  border: 1px solid var(--rule);
  border-radius: 6px;
  padding: 0.9rem 1.1rem;
  overflow: auto;
  max-width: 54rem;
  margin: 1rem 0;
  position: relative;
}
pre code {
  background: none;
  border: 0;
  padding: 0;
  font-size: 0.82rem;
  line-height: 1.6;
}
pre.mermaid {
  text-align: center;
  padding: 1.25rem;
  background: var(--surface);
}
blockquote {
  margin: 1.1rem 0;
  padding: 0.65rem 1rem;
  background: var(--surface);
  border: 1px solid var(--rule);
  border-left: 3px solid var(--accent-blue);
  border-radius: 4px;
  color: var(--dim);
}
blockquote p { margin: 0.25rem 0; }
table {
  border-collapse: collapse;
  margin: 1.2rem 0;
  font-size: 0.88rem;
  width: 100%;
  max-width: 54rem;
  background: var(--surface);
  border: 1px solid var(--rule);
  border-radius: 6px;
  overflow: hidden;
}
th, td {
  border: 1px solid var(--rule);
  padding: 0.5rem 0.75rem;
  text-align: left;
  vertical-align: top;
}
th {
  background: var(--surface-alt);
  font-family: var(--font-mono);
  font-size: 0.76rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--dim);
  font-weight: 700;
}
hr {
  border: 0;
  border-top: 1px solid var(--rule);
  margin: 2rem 0;
}
.repo { color: var(--dim); }
.repo code { font-size: 0.82em; }
.lede {
  color: var(--dim);
  font-size: 1.02rem;
  line-height: 1.65;
}
.sym {
  list-style: none;
  padding: 0;
  margin: 0.5rem 0 1.4rem;
  border: 1px solid var(--rule);
  border-radius: 6px;
  background: var(--surface);
  overflow: hidden;
}
.sym li {
  margin: 0;
  padding: 0.42rem 0.85rem;
  border-bottom: 1px solid var(--rule);
  max-width: none;
}
.sym li:last-child { border-bottom: 0; }
.sym .m {
  padding-left: 2rem;
  background: var(--code);
}
.sym code {
  background: none;
  border: 0;
  padding: 0;
  font-size: 0.82rem;
}
.api-filter-bar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin: 1rem 0 1.5rem;
}
.api-filter-input {
  width: 100%;
  max-width: 28rem;
  background: var(--code);
  border: 1px solid var(--rule);
  border-radius: 4px;
  padding: 0.5rem 0.85rem;
  color: var(--ink);
  font: 0.84rem var(--font-mono);
}
.api-filter-input:focus {
  outline: none;
  border-color: var(--accent-blue);
}
footer {
  color: var(--dim);
  font-size: 0.82rem;
  border-top: 1px solid var(--rule);
  padding: 1.5rem 0;
  margin-top: 3.5rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 0.75rem;
}
footer .footer-brand {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  font-family: var(--font-mono);
  font-size: 0.78rem;
}

/* Landing Page (Tako Kamon & Dual-Theme Engineering Layout) */
.memo-container {
  max-width: 74rem;
  margin: 0 auto;
  width: 100%;
}
.memo-section-header {
  font-family: var(--font-mono);
  font-size: 0.76rem;
  font-weight: 700;
  text-transform: uppercase;
  color: var(--dim);
  letter-spacing: 0.07em;
  margin-bottom: 0.75rem;
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
}
.memo-hero {
  padding: 2.75rem 0 3rem;
  border-bottom: 1px solid var(--rule);
}
.hero-split-grid {
  display: grid;
  grid-template-columns: 1.08fr 0.92fr;
  gap: 2.25rem;
  align-items: start;
}
.hero-eyebrow-row {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  flex-wrap: wrap;
  margin-bottom: 1rem;
}
.eyebrow-badge {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  font-family: var(--font-mono);
  font-size: 0.72rem;
  font-weight: 700;
  letter-spacing: 0.06em;
  padding: 0.25rem 0.6rem;
  border-radius: 4px;
  background: var(--surface);
  border: 1px solid var(--rule);
  color: var(--dim);
}
.eyebrow-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--accent-emerald);
}
.handwritten-sticker {
  font-family: var(--font-mono);
  font-size: 0.74rem;
  font-weight: 700;
  color: var(--primary);
  background: var(--primary-bg);
  border: 1px solid var(--primary-border);
  padding: 0.22rem 0.6rem;
  border-radius: 4px;
  white-space: nowrap;
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
}
.memo-hero-title {
  font-size: clamp(2.1rem, 4.5vw, 3.1rem);
  font-weight: 800;
  letter-spacing: -0.04em;
  line-height: 1.12;
  margin: 0 0 1.1rem;
}
.flat-vermilion {
  color: var(--primary);
  border-bottom: 2px solid var(--primary);
}
.memo-hero-desc {
  font-size: 1.02rem;
  line-height: 1.65;
  color: var(--dim);
  max-width: 40rem;
  margin: 0 0 1.5rem;
}
.memo-hero-desc strong { color: var(--ink); }

.memo-actions {
  display: flex;
  gap: 0.65rem;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 1.35rem;
}
.m-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  padding: 0.62rem 1.15rem;
  font-family: var(--font-mono);
  font-size: 0.82rem;
  font-weight: 600;
  border-radius: 4px;
  text-decoration: none !important;
  transition: all 0.12s ease;
  cursor: pointer;
  border: 1px solid transparent;
}
.m-btn-primary {
  background-color: var(--primary);
  color: #ffffff !important;
  border-color: var(--primary);
}
.m-btn-primary:hover {
  background-color: var(--primary-hover);
  border-color: var(--primary-hover);
}
.m-btn-secondary {
  background-color: var(--surface);
  color: var(--ink) !important;
  border-color: var(--rule);
}
.m-btn-secondary:hover {
  background-color: var(--surface-hover);
  border-color: var(--rule-dark);
}
.m-btn-link {
  color: var(--accent-blue) !important;
  font-weight: 600;
  padding: 0.62rem 0.4rem;
}
.m-btn-link:hover { text-decoration: underline !important; }

.memo-install-box {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  background-color: var(--code);
  border: 1px solid var(--rule);
  padding: 0.55rem 0.85rem;
  border-radius: 6px;
  font-family: var(--font-mono);
  font-size: 0.82rem;
  max-width: 36rem;
  overflow: hidden;
  margin-bottom: 0.9rem;
}
.terminal-bar {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  overflow-x: auto;
  white-space: nowrap;
}
.terminal-prompt {
  color: var(--accent-emerald);
  font-weight: 700;
  flex-shrink: 0;
}
.memo-install-box code {
  background: none !important;
  border: 0 !important;
  padding: 0 !important;
  color: var(--ink) !important;
  font-size: 0.8rem;
}
.hero-guarantees-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 0.85rem;
  font-family: var(--font-mono);
  font-size: 0.74rem;
  color: var(--dim);
}
.hero-guarantees-strip span strong {
  color: var(--accent-emerald);
}

/* Hero Right: Interactive Code-to-Cloud Synthesis Card */
.hero-synth-card {
  border: 1px solid var(--rule);
  border-radius: 6px;
  background: var(--code);
  overflow: hidden;
}
.hero-synth-card .code-tabs {
  margin: 0;
  border: 0;
  border-radius: 0;
  max-width: none;
}
.hero-synth-card pre {
  padding: 0.85rem 1rem;
  font-size: 0.78rem;
}
.synth-drawer {
  border-top: 1px solid var(--rule);
  background: var(--surface);
  padding: 0.75rem 0.95rem;
  font-family: var(--font-mono);
  font-size: 0.74rem;
}
.synth-drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: var(--tertiary);
  font-size: 0.68rem;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  margin-bottom: 0.5rem;
}
.synth-drawer-header .status {
  color: var(--accent-emerald);
}
.synth-row {
  display: flex;
  align-items: baseline;
  gap: 0.55rem;
  padding: 0.32rem 0.55rem;
  border: 1px solid var(--rule);
  border-radius: 4px;
  background: var(--code);
  margin-top: 0.35rem;
  line-height: 1.45;
}
.synth-row .lbl-local {
  color: var(--accent-emerald);
  font-weight: 700;
  flex-shrink: 0;
}
.synth-row .lbl-cloud {
  color: var(--accent-blue);
  font-weight: 700;
  flex-shrink: 0;
}
.synth-row .val {
  color: var(--dim);
}

.memo-section {
  padding: 2.75rem 0;
  border-bottom: 1px solid var(--rule);
}
.memo-section-title {
  font-size: 1.55rem;
  font-weight: 750;
  letter-spacing: -0.03em;
  color: var(--ink);
  margin: 0 0 0.4rem;
  border: none !important;
  padding: 0 !important;
}
.memo-section-sub {
  color: var(--dim);
  font-size: 0.94rem;
  max-width: 46rem;
  margin: 0 0 1.6rem;
  line-height: 1.6;
}

.memo-project-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
  gap: 1rem;
}
.memo-card {
  background-color: var(--surface);
  border: 1px solid var(--rule);
  border-radius: 6px;
  padding: 1.25rem;
  text-decoration: none !important;
  color: inherit !important;
  display: flex;
  flex-direction: column;
  transition: border-color 0.12s ease, background-color 0.12s ease;
}
.memo-card:hover {
  text-decoration: none !important;
  background-color: var(--surface-hover);
}
.memo-card.card-blue:hover { border-color: var(--accent-blue); }
.memo-card.card-emerald:hover { border-color: var(--accent-emerald); }
.memo-card.card-purple:hover { border-color: var(--accent-purple); }
.memo-card.card-amber:hover { border-color: var(--accent-amber); }
.memo-card.card-pink:hover { border-color: var(--primary); }
.memo-card.card-cyan:hover { border-color: var(--accent-cyan); }

.card-top {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  margin-bottom: 0.7rem;
}
.card-id {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  font-weight: 700;
  color: var(--tertiary);
  border: 1px solid var(--rule);
  background: var(--code);
  padding: 0.12rem 0.4rem;
  border-radius: 3px;
}
.badge {
  display: inline-block;
  padding: 0.16rem 0.48rem;
  font-size: 0.68rem;
  font-family: var(--font-mono);
  font-weight: 700;
  letter-spacing: 0.04em;
  border-radius: 3px;
}
.badge-blue { background: var(--accent-blue-bg); color: var(--accent-blue); border: 1px solid var(--accent-blue-border); }
.badge-emerald { background: var(--accent-emerald-bg); color: var(--accent-emerald); border: 1px solid var(--accent-emerald-border); }
.badge-purple { background: var(--accent-purple-bg); color: var(--accent-purple); border: 1px solid var(--accent-purple-border); }
.badge-amber { background: var(--accent-amber-bg); color: var(--accent-amber); border: 1px solid var(--accent-amber-border); }
.badge-pink { background: var(--primary-bg); color: var(--primary); border: 1px solid var(--primary-border); }
.badge-cyan { background: var(--accent-cyan-bg); color: var(--accent-cyan); border: 1px solid var(--accent-cyan-border); }

.card-status {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: 0.66rem;
  color: var(--tertiary);
}
.card-title {
  margin: 0 0 0.4rem !important;
  font-size: 1.1rem !important;
  font-weight: 700;
  border: none !important;
  padding: 0 !important;
}
.card-title code {
  background: none !important;
  border: 0 !important;
  padding: 0 !important;
  font-family: var(--font-mono);
  color: var(--ink);
}
.card-desc {
  color: var(--dim);
  font-size: 0.85rem;
  line-height: 1.55;
  flex: 1;
  margin: 0 0 1rem;
}
.card-tags {
  display: flex;
  gap: 0.35rem;
  flex-wrap: wrap;
  margin-bottom: 0.85rem;
}
.tag {
  display: inline-block;
  padding: 0.14rem 0.42rem;
  font-size: 0.7rem;
  background-color: var(--code);
  color: var(--dim);
  border: 1px solid var(--rule);
  font-family: var(--font-mono);
  border-radius: 3px;
}
.card-footer-meta {
  display: flex;
  justify-content: space-between;
  border-top: 1px solid var(--rule);
  padding-top: 0.65rem;
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--tertiary);
}
.card-footer-meta span:first-child { color: var(--accent-emerald); }
.card-footer-meta span:last-child { color: var(--accent-blue); }

.two-stories-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1.15rem;
}
.story-panel {
  border-radius: 6px;
  padding: 1.5rem;
  text-decoration: none !important;
  color: inherit !important;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--rule);
  transition: border-color 0.12s ease;
}
.local-panel {
  background-color: var(--story-local-bg);
  border-color: var(--story-local-border);
  border-left: 3px solid var(--accent-emerald);
}
.local-panel:hover { border-color: var(--accent-emerald); }
.cloud-panel {
  background-color: var(--story-cloud-bg);
  border-color: var(--story-cloud-border);
  border-left: 3px solid var(--accent-blue);
}
.cloud-panel:hover { border-color: var(--accent-blue); }

.panel-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.65rem;
}
.pill-dot { width: 8px; height: 8px; border-radius: 50%; }
.dot-emerald { background-color: var(--accent-emerald); }
.dot-blue { background-color: var(--accent-blue); }
.panel-tag {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  font-weight: 700;
  letter-spacing: 0.06em;
  color: var(--dim);
}
.story-panel h3 {
  font-size: 1.25rem;
  font-weight: 700;
  margin: 0 0 0.6rem;
  border: none !important;
  padding: 0 !important;
  color: var(--ink);
}
.story-panel p {
  color: var(--dim);
  font-size: 0.88rem;
  line-height: 1.6;
  flex: 1;
  margin: 0 0 1.15rem;
}
.panel-stat-row {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 0.65rem;
  margin-bottom: 1rem;
}
.p-stat {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--dim);
  background: var(--surface);
  border: 1px solid var(--rule);
  border-radius: 4px;
  padding: 0.5rem 0.65rem;
}
.p-stat strong {
  display: block;
  color: var(--ink);
  font-size: 0.92rem;
  margin-bottom: 0.1rem;
}
.panel-link {
  font-family: var(--font-mono);
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--accent-blue);
}

.tutorial-list {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.tutorial-item {
  background-color: var(--surface);
  border: 1px solid var(--rule);
  border-radius: 6px;
  padding: 0.85rem 1.1rem;
  display: flex;
  align-items: center;
  gap: 1rem;
  text-decoration: none !important;
  color: inherit !important;
  transition: border-color 0.12s ease, background-color 0.12s ease;
}
.tutorial-item:hover {
  border-color: var(--primary);
  background-color: var(--surface-hover);
}
.t-num {
  font-family: var(--font-mono);
  font-size: 0.82rem;
  font-weight: 700;
  color: var(--primary);
  background: var(--primary-bg);
  border: 1px solid var(--primary-border);
  width: 2rem;
  height: 2rem;
  border-radius: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.t-body { flex: 1; min-width: 0; }
.t-title {
  font-family: var(--font-mono);
  font-weight: 650;
  font-size: 0.9rem;
  color: var(--ink);
  margin-bottom: 0.15rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.t-desc {
  font-size: 0.82rem;
  color: var(--dim);
  line-height: 1.45;
}
.t-tags { display: flex; gap: 0.35rem; flex-shrink: 0; }

/* Responsive Reflow */
@media (max-width: 1180px) {
  aside.toc { display: none !important; }
}
@media (max-width: 960px) {
  .hero-split-grid { grid-template-columns: 1fr; gap: 1.5rem; }
}
@media (max-width: 768px) {
  .layout { flex-direction: column !important; padding: 0 1rem !important; gap: 0.75rem !important; }
  .layout.layout-home nav.side { display: none !important; }
  main { padding: 1rem 0 3.5rem !important; width: 100% !important; max-width: 100% !important; }
  header.top-nav {
    padding: 0 0.85rem !important;
    gap: 0.55rem !important;
    overflow-x: auto !important;
    -webkit-overflow-scrolling: touch !important;
    scrollbar-width: none !important;
    white-space: nowrap !important;
  }
  header.top-nav::-webkit-scrollbar { display: none; }
  header .brand-docs-sub { display: none; }
  header .nav-links a { padding: 0 0.5rem; font-size: 0.78rem; }
  header .github-link span { display: none; }
  header .github-link { padding: 0.28rem 0.45rem; }
  .memo-hero { padding: 1.5rem 0 2rem; }
  .memo-hero-title { font-size: 2rem; line-height: 1.16; }
  .memo-hero-desc { font-size: 0.95rem; line-height: 1.55; }
  .memo-actions { flex-direction: column; width: 100%; gap: 0.55rem; }
  .m-btn { width: 100%; justify-content: center; padding: 0.75rem 1rem; }
  .memo-install-box { width: 100%; max-width: 100%; font-size: 0.75rem; padding: 0.5rem 0.65rem; }
  .memo-install-box code { font-size: 0.74rem; }
  .synth-row { flex-direction: column; gap: 0.2rem; }
  .memo-project-grid { grid-template-columns: 1fr; }
  .two-stories-grid { grid-template-columns: 1fr; }
  .panel-stat-row { grid-template-columns: 1fr; gap: 0.45rem; }
  .tutorial-item { flex-direction: column; align-items: flex-start; gap: 0.55rem; padding: 0.9rem; }
  .t-title { white-space: normal; }
  .t-tags { flex-wrap: wrap; }

  nav.side {
    width: 100% !important;
    position: sticky !important;
    top: 3.5rem !important;
    background: var(--bg) !important;
    z-index: 40 !important;
    display: flex !important;
    overflow-x: auto !important;
    -webkit-overflow-scrolling: touch !important;
    gap: 0.4rem !important;
    padding: 0.55rem 0 !important;
    border-right: 0 !important;
    border-bottom: 1px solid var(--rule) !important;
    scrollbar-width: none !important;
    white-space: nowrap !important;
  }
  nav.side::-webkit-scrollbar { display: none; }
  nav.side a {
    display: inline-flex !important;
    align-items: center !important;
    padding: 0.3rem 0.7rem !important;
    margin: 0 !important;
    background-color: var(--surface) !important;
    border: 1px solid var(--rule) !important;
    border-radius: 4px !important;
    font-size: 0.78rem !important;
    flex-shrink: 0 !important;
  }
  nav.side a.on {
    background-color: var(--primary) !important;
    color: #ffffff !important;
    border-color: var(--primary) !important;
  }
  nav.side .group { display: none !important; }
  h1 { font-size: 1.55rem !important; }
  h2 { font-size: 1.2rem !important; margin: 1.75rem 0 0.6rem !important; }
  pre { max-width: 100% !important; overflow-x: auto !important; -webkit-overflow-scrolling: touch !important; padding: 0.75rem !important; font-size: 0.78rem !important; }
  table { display: block !important; overflow-x: auto !important; -webkit-overflow-scrolling: touch !important; max-width: 100% !important; font-size: 0.8rem !important; }
}
`

var tmpl = template.Must(template.New("docs").Parse(`
{{define "head"}}<!doctype html>
<html lang="en" data-theme="dark"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{if .Title}}{{.Title}} · {{end}}tako docs</title>
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
<script>
(function() {
  try {
    var saved = localStorage.getItem('tako-doc-theme');
    var theme = (saved === 'light' || saved === 'dark')
      ? saved
      : (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
    document.documentElement.setAttribute('data-theme', theme);
  } catch (_) {}
})();
</script>
<style>{{.Style}}</style></head><body>
<header class="top-nav">
  <a href="/" class="brand-link" title="tako docs — Polyglot Cloud Framework (蛸)">
    <svg class="brand-kamon" viewBox="0 0 160 160" fill="none" aria-hidden="true">
      <circle cx="80" cy="80" r="74" stroke="var(--primary)" stroke-width="5"/>
      <circle cx="80" cy="80" r="65" stroke="var(--primary)" stroke-width="2" stroke-dasharray="5 5" opacity="0.65"/>
      <path fill-rule="evenodd" clip-rule="evenodd" d="M58 72 C58 50, 102 50, 102 72 C102 85, 92 90, 80 90 C68 90, 58 85, 58 72 Z M69 65 A4.5 4.5 0 1 0 69 74 A4.5 4.5 0 1 0 69 65 Z M91 65 A4.5 4.5 0 1 0 91 74 A4.5 4.5 0 1 0 91 65 Z" fill="var(--primary)"/>
      <path d="M60 64 L42 46 L28 46 M100 64 L118 46 L132 46 M57 77 L38 77 L24 90 M103 77 L122 77 L136 90 M72 90 L66 110 L45 124 M88 90 L94 110 L115 124 M77 90 L74 124 L65 136 M83 90 L86 124 L95 136" stroke="var(--primary)" stroke-width="4.5" stroke-linecap="round" stroke-linejoin="round"/>
      <circle cx="28" cy="46" r="5" fill="var(--primary)"/><circle cx="132" cy="46" r="5" fill="var(--primary)"/>
      <circle cx="24" cy="90" r="5" fill="var(--primary)"/><circle cx="136" cy="90" r="5" fill="var(--primary)"/>
      <circle cx="45" cy="124" r="5" fill="var(--primary)"/><circle cx="115" cy="124" r="5" fill="var(--primary)"/>
      <circle cx="65" cy="136" r="5" fill="var(--primary)"/><circle cx="95" cy="136" r="5" fill="var(--primary)"/>
    </svg>
    <span class="brand">tako</span>
    <span class="hanko-stamp" title="Tako (蛸) — Octopus in Japanese">蛸</span>
    <span class="brand-docs-sub">tako docs</span>
  </a>
  <nav class="nav-links" aria-label="Primary sections">
    <a href="/guide/"{{if eq .Section "guide"}} class="on"{{end}}>guide</a>
    <a href="/reference/"{{if eq .Section "reference"}} class="on"{{end}}>reference</a>
    <a href="/api"{{if eq .Section "api"}} class="on"{{end}}>api</a>
    <a href="/llms.txt">llms.txt</a>
  </nav>
  <div class="header-right">
    <div class="header-lang-switch" role="tablist" aria-label="Preferred code language" title="Switch all code examples between Go and cfire">
      <button type="button" class="header-lang-btn on" data-lang="go" role="tab" aria-selected="true">Go</button>
      <button type="button" class="header-lang-btn" data-lang="cfire" role="tab" aria-selected="false">cfire</button>
    </div>
    <div class="header-theme-switch" role="group" aria-label="Color theme" title="Toggle between Technical Paper Light and Obsidian Dark theme">
      <button type="button" class="header-theme-btn" data-theme-btn="light" aria-label="Light theme">☀ Light</button>
      <button type="button" class="header-theme-btn on" data-theme-btn="dark" aria-label="Dark theme">☾ Dark</button>
    </div>
    <a href="https://github.com/gsandoval/tako" target="_blank" rel="noopener noreferrer" class="github-link" title="GitHub Repository">
      <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
      <span>GitHub</span>
    </a>
  </div>
</header>
<div class="layout{{if eq .Section ""}} layout-home{{end}}">
<nav class="side" aria-label="Section navigation">{{range .Nav}}{{if .Group}}<div class="group">{{.Title}}</div>{{else}}<a href="{{.Route}}"{{if .On}} class="on"{{end}}>{{.Title}}</a>{{end}}{{end}}</nav>
<main>{{end}}

{{define "foot"}}<footer>
  <span class="footer-brand">
    <img src="/assets/tako_logo_emblem.svg" alt="Tako Kamon" width="18" height="18">
    <span>{{.Footer}}</span>
  </span>
  <span><a href="/llms.txt">llms.txt</a> · <a href="/llms-full.txt">llms-full.txt</a> · <a href="https://github.com/gsandoval/tako" target="_blank" rel="noopener noreferrer">GitHub</a></span>
</footer></main>
{{if .TOC}}
<aside class="toc" aria-label="On this page">
  <div class="toc-header">On This Page</div>
  <ul class="toc-list">
    {{range .TOC}}<li><a href="#{{.ID}}" class="{{if eq .Level 3}}toc-h3{{else}}toc-h2{{end}}" data-toc-id="{{.ID}}">{{.Title}}</a></li>{{end}}
  </ul>
  <div class="toc-tools">
    {{if .CLIHint}}<span class="toc-tool-link" title="Read in terminal">$ {{.CLIHint}}</span>{{end}}
    <a href="/api" class="toc-tool-link">SDK Surface Lock →</a>
    <a href="/llms-full.txt" class="toc-tool-link">LLM Context Bundle →</a>
  </div>
</aside>
{{end}}
</div>
<script type="module">
const STORAGE_KEY = 'tako-doc-lang';
const THEME_KEY = 'tako-doc-theme';

function syncThemeButtons(theme) {
  for (const btn of document.querySelectorAll('.header-theme-btn')) {
    const active = btn.getAttribute('data-theme-btn') === theme;
    btn.classList.toggle('on', active);
    btn.setAttribute('aria-pressed', active ? 'true' : 'false');
  }
}

function setDocTheme(theme) {
  if (theme !== 'light' && theme !== 'dark') theme = 'dark';
  document.documentElement.setAttribute('data-theme', theme);
  syncThemeButtons(theme);
  try { localStorage.setItem(THEME_KEY, theme); } catch (_) {}
}

syncThemeButtons(document.documentElement.getAttribute('data-theme') || 'dark');

function setDocLang(lang, anchorEl) {
  if (lang !== 'go' && lang !== 'cfire') lang = 'go';
  const prevTop = anchorEl ? anchorEl.getBoundingClientRect().top : null;
  for (const tabs of document.querySelectorAll('.code-tabs')) {
    tabs.setAttribute('data-active-lang', lang);
    for (const btn of tabs.querySelectorAll('.code-tab-btn')) {
      const active = btn.getAttribute('data-lang') === lang;
      btn.classList.toggle('on', active);
      btn.setAttribute('aria-selected', active ? 'true' : 'false');
    }
    for (const cap of tabs.querySelectorAll('.code-tab-caption')) {
      cap.classList.toggle('on', cap.getAttribute('data-lang') === lang);
    }
    for (const pane of tabs.querySelectorAll('.code-tab-pane')) {
      pane.classList.toggle('on', pane.getAttribute('data-lang') === lang);
    }
  }
  for (const hbtn of document.querySelectorAll('.header-lang-btn')) {
    const active = hbtn.getAttribute('data-lang') === lang;
    hbtn.classList.toggle('on', active);
    hbtn.setAttribute('aria-selected', active ? 'true' : 'false');
  }
  try { localStorage.setItem(STORAGE_KEY, lang); } catch (_) {}
  if (anchorEl && prevTop !== null) {
    const nextTop = anchorEl.getBoundingClientRect().top;
    window.scrollBy(0, nextTop - prevTop);
  }
}

const urlLang = new URLSearchParams(window.location.search).get('lang');
let initialLang = 'go';
try {
  initialLang = urlLang || localStorage.getItem(STORAGE_KEY) || 'go';
} catch (_) {}
setDocLang(initialLang, null);

document.addEventListener('click', async (e) => {
  const themeBtn = e.target.closest('.header-theme-btn');
  if (themeBtn) {
    setDocTheme(themeBtn.getAttribute('data-theme-btn'));
    return;
  }
  const btn = e.target.closest('.code-tab-btn, .header-lang-btn');
  if (btn) {
    const lang = btn.getAttribute('data-lang');
    const anchor = btn.closest('.code-tabs');
    setDocLang(lang, anchor);
    return;
  }
  const copyBtn = e.target.closest('.code-copy-btn, .install-copy-btn');
  if (copyBtn) {
    let text = copyBtn.getAttribute('data-copy') || '';
    if (!text) {
      const tabs = copyBtn.closest('.code-tabs');
      if (tabs) {
        const activePane = tabs.querySelector('.code-tab-pane.on pre code') || tabs.querySelector('pre code');
        if (activePane) text = activePane.textContent.trim();
      }
    }
    if (text && navigator.clipboard) {
      try {
        await navigator.clipboard.writeText(text);
        const orig = copyBtn.textContent;
        copyBtn.textContent = 'Copied ✓';
        copyBtn.classList.add('copied');
        setTimeout(() => {
          copyBtn.textContent = orig;
          copyBtn.classList.remove('copied');
        }, 1400);
      } catch (_) {}
    }
  }
});

// Scrollspy for Right-Rail TOC
const tocLinks = Array.from(document.querySelectorAll('aside.toc a[data-toc-id]'));
if (tocLinks.length > 0 && 'IntersectionObserver' in window) {
  const headings = tocLinks.map(a => document.getElementById(a.getAttribute('data-toc-id'))).filter(Boolean);
  const obs = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (entry.isIntersecting) {
        const id = entry.target.id;
        for (const a of tocLinks) {
          a.classList.toggle('active', a.getAttribute('data-toc-id') === id);
        }
        break;
      }
    }
  }, { rootMargin: '-70px 0px -75% 0px', threshold: 0 });
  for (const h of headings) obs.observe(h);
}

// API Surface Quick Filter
const apiInput = document.getElementById('api-symbol-filter');
if (apiInput) {
  apiInput.addEventListener('input', () => {
    const q = apiInput.value.trim().toLowerCase();
    for (const ul of document.querySelectorAll('ul.sym')) {
      let anyVisible = false;
      for (const li of ul.querySelectorAll('li')) {
        const match = !q || li.textContent.toLowerCase().includes(q);
        li.style.display = match ? '' : 'none';
        if (match) anyVisible = true;
      }
      const prevH3 = ul.previousElementSibling;
      if (prevH3 && prevH3.tagName === 'H3') {
        prevH3.style.display = anyVisible ? '' : 'none';
      }
      ul.style.display = anyVisible ? '' : 'none';
    }
  });
}

const blocks = document.querySelectorAll('pre > code.language-mermaid');
if (blocks.length > 0) {
  const nodes = [];
  for (const code of blocks) {
    const pre = code.parentElement;
    pre.classList.add('mermaid');
    pre.textContent = code.textContent.replace(/\\n/g, '<br/>');
    nodes.push(pre);
  }
  try {
    const { default: mermaid } = await import('https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs');
    const isDark = document.documentElement.getAttribute('data-theme') === 'dark';
    mermaid.initialize({ startOnLoad: false, theme: isDark ? 'dark' : 'neutral', securityLevel: 'strict' });
    for (const node of nodes) {
      try {
        await mermaid.run({ nodes: [node] });
      } catch (_) {}
    }
  } catch (_) {}
}
</script>
</body></html>{{end}}

{{define "page"}}{{template "head" .}}
<div class="doc-meta-bar">
  <div class="doc-breadcrumb"><span>{{.Section}}</span> / {{if .Name}}{{.Name}}{{else}}overview{{end}}</div>
  {{if .CLIHint}}
  <button type="button" class="doc-cli-pill code-copy-btn" data-copy="{{.CLIHint}}" title="Copy CLI doc lookup command">
    <span class="prompt">$</span> <span>{{.CLIHint}}</span>
  </button>
  {{end}}
</div>
{{.Body}}{{template "foot" .}}{{end}}

{{define "home"}}{{template "head" .}}
<div class="memo-container">
  <section class="memo-hero">
    <div class="hero-split-grid">
      <div class="hero-left">
        <div class="hero-eyebrow-row">
          <span class="eyebrow-badge"><span class="eyebrow-dot"></span> // 0x00 POLYGLOT CLOUD RUNTIME</span>
          <span class="handwritten-sticker">蛸 · invisible infrastructure</span>
        </div>

        <h1 class="memo-hero-title">
          Write Go or cfire.<br><span class="flat-vermilion">Get a cloud.</span>
        </h1>

        <p class="memo-hero-desc">
          <strong>Transparent distributed systems over Google Cloud Run.</strong> Stop repeating compute footprints, IAM permissions, and deployment configuration across YAML and Terraform: declare the architectural intent in your <strong>Go</strong> or <strong><a href="https://cfire.memo.engineer" target="_blank" rel="noopener noreferrer">cfire</a></strong> (<code>.cf</code>, <a href="https://cfire.memo.engineer/guide.html#chapter-1" target="_blank" rel="noopener noreferrer">1:1 ISO C++23</a>) code, run the whole stack locally in one process, and let Tako derive the cloud.
        </p>

        <div class="memo-actions">
          <a href="/guide/01-hello" class="m-btn m-btn-primary">
            ⚡ Get Started (5 min)
          </a>
          <a href="/guide/00-mental-model" class="m-btn m-btn-secondary">Mental Model</a>
          <a href="/reference/" class="m-btn m-btn-secondary">Explore Reference</a>
          <a href="/guide/local" class="m-btn m-btn-link">The Local Story →</a>
        </div>

        <div class="memo-install-box">
          <div class="terminal-bar">
            <span class="terminal-prompt">$</span>
            <code>go install github.com/gsandoval/tako/cmd/tako@latest</code>
          </div>
          <button type="button" class="code-copy-btn install-copy-btn" data-copy="go install github.com/gsandoval/tako/cmd/tako@latest">Copy</button>
        </div>

        <div class="hero-guarantees-strip">
          <span><strong>✓</strong> 0 YAML / Terraform</span>
          <span><strong>✓</strong> 1 Local Process</span>
          <span><strong>✓</strong> 100% Derived IAM</span>
          <span><strong>✓</strong> Cloud Run Native</span>
        </div>
      </div>

      <div class="hero-right">
        <div class="hero-synth-card">
          <div class="code-tabs" data-active-lang="go">
            <div class="code-tabs-bar">
              <div class="code-tabs-switch" role="tablist" aria-label="Hero code language">
                <button type="button" class="code-tab-btn on" data-lang="go" role="tab" aria-selected="true">Go</button>
                <button type="button" class="code-tab-btn" data-lang="cfire" role="tab" aria-selected="false">cfire</button>
              </div>
              <div class="code-tabs-meta">
                <span class="code-tab-caption on" data-lang="go">hello/app.go</span>
                <span class="code-tab-caption" data-lang="cfire">hello/app.cf</span>
              </div>
              <button type="button" class="code-copy-btn" title="Copy snippet">Copy</button>
            </div>
            <div class="code-tab-pane on" data-lang="go"><pre><code class="language-go">package hello

import "github.com/gsandoval/tako"

var Greet = tako.API("GET /greet/{name}", greet)
var Links = tako.Entity[Link]("links")

type GreetReq struct {
    Name string ` + "`" + `path:"name"` + "`" + `
}

func greet(ctx tako.Ctx, req GreetReq) (Greeting, error) {
    return Greeting{Message: "Hello, " + req.Name + "!"}, nil
}</code></pre></div>
            <div class="code-tab-pane" data-lang="cfire"><pre><code class="language-cfire">package hello

import "core:tako"

Greet := tako.api("GET /greet/{name}", greet_handler)
Links := tako.entity[Link]("links")

type GreetReq struct {
    name: str ` + "`" + `path:"name"` + "`" + `
}

fn greet_handler(ctx: tako.Ctx, req: GreetReq) -&gt; Greeting! {
    return Greeting{message: "Hello, " + req.name + "!"}
}</code></pre></div>
          </div>
          <div class="synth-drawer">
            <div class="synth-drawer-header">
              <span>● STATIC ANALYSIS SYNTHESIS (tako check)</span>
              <span class="status">DETERMINISTIC</span>
            </div>
            <div class="synth-row">
              <span class="lbl-local">LOCAL:</span>
              <span class="val">1 process · embedded bbolt (.tako/data) · in-memory channels</span>
            </div>
            <div class="synth-row">
              <span class="lbl-cloud">CLOUD:</span>
              <span class="val">Cloud Run (hello) + GCP Datastore (links) + Least-Privilege IAM</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>

  <section class="memo-section">
    <div class="memo-section-header">// 0x01 Core Primitives (Declared Nouns)</div>
    <h2 class="memo-section-title">The Declared Vocabulary</h2>
    <p class="memo-section-sub">
      Package-level declarations in Go or cfire, checked by a static analyzer before anything builds. No comment directives, no reflection in the request path, no YAML on the side.
    </p>

    <div class="memo-project-grid">
      <a href="/reference/api" class="memo-card card-blue">
        <div class="card-top">
          <span class="card-id">PRM-01</span>
          <span class="badge badge-blue">HTTP &amp; RPC</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.API</code> &amp; <code>.Call</code></h3>
        <p class="card-desc">Typed JSON endpoints with path and query binding. Export the var and another package calls it directly as a private Cloud Run service — no protobufs or client codegen.</p>
        <div class="card-tags">
          <span class="tag">Cloud Run</span>
          <span class="tag">Route.Private</span>
          <span class="tag">TS Client</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: Direct call</span>
          <span>Cloud: Cloud Run + IAM</span>
        </div>
      </a>

      <a href="/reference/site" class="memo-card card-cyan">
        <div class="card-top">
          <span class="card-id">PRM-02</span>
          <span class="badge badge-cyan">WEB &amp; HTMX</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Site</code> &amp; <code>Page</code></h3>
        <p class="card-desc">Server-rendered HTML templates, CSRF-checked forms, HTMX fragments, and custom <code>StatusPage</code> handlers — or an Astro/Hugo build embedded in the binary. Same origin as your API, so CORS never comes up.</p>
        <div class="card-tags">
          <span class="tag">HTMX</span>
          <span class="tag">StatusPage</span>
          <span class="tag">Custom Domain</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: Live reload</span>
          <span>Cloud: Cloud Run + TLS</span>
        </div>
      </a>

      <a href="/reference/auth" class="memo-card card-emerald">
        <div class="card-top">
          <span class="card-id">PRM-03</span>
          <span class="badge badge-emerald">IDENTITY &amp; RBAC</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>Auth</code>, <code>Policy</code> &amp; <code>Role</code></h3>
        <p class="card-desc">One declaration mounts sign-in, revocable server-side sessions, CSRF tokens, and RBAC policies (<code>RequireRole</code>, <code>auth.Users()</code>). Injects a one-click persona bar locally.</p>
        <div class="card-tags">
          <span class="tag">Identity Platform</span>
          <span class="tag">Policy &amp; Role</span>
          <span class="tag">Dev Bar</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: Persona dev bar</span>
          <span>Cloud: Identity Platform</span>
        </div>
      </a>

      <a href="/reference/entity" class="memo-card card-emerald">
        <div class="card-top">
          <span class="card-id">PRM-04</span>
          <span class="badge badge-emerald">DATASTORE</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Entity[T]</code></h3>
        <p class="card-desc">Typed document storage with transactions and queries. The analyzer reads your <code>.Where()</code> and <code>.Order()</code> calls and derives the composite indexes for you.</p>
        <div class="card-tags">
          <span class="tag">Datastore</span>
          <span class="tag">bbolt</span>
          <span class="tag">Derived Indexes</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: .tako/data/entity.db</span>
          <span>Cloud: GCP Datastore</span>
        </div>
      </a>

      <a href="/reference/bucket" class="memo-card card-amber">
        <div class="card-top">
          <span class="card-id">PRM-05</span>
          <span class="badge badge-amber">BLOBS</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Bucket</code></h3>
        <p class="card-desc">Object storage with streaming I/O, presigned upload/download URLs, and upload notifications. Bucket IAM is scoped to the handlers that actually touch it.</p>
        <div class="card-tags">
          <span class="tag">Cloud Storage</span>
          <span class="tag">Signed URLs</span>
          <span class="tag">Upload Events</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: .tako/data/buckets/</span>
          <span>Cloud: Cloud Storage</span>
        </div>
      </a>

      <a href="/reference/queue" class="memo-card card-purple">
        <div class="card-top">
          <span class="card-id">PRM-06</span>
          <span class="badge badge-purple">ASYNC &amp; EVENTS</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>Queue[T]</code> &amp; <code>Topic[T]</code></h3>
        <p class="card-desc">Competing workers via Queue and fan-out broadcasts via Topic. Locally they are channels calling your functions, so a breakpoint steps straight across the publish.</p>
        <div class="card-tags">
          <span class="tag">Cloud Tasks</span>
          <span class="tag">Pub/Sub</span>
          <span class="tag">Dead Letters</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: Go channels</span>
          <span>Cloud: Tasks &amp; Pub/Sub</span>
        </div>
      </a>

      <a href="/reference/cron" class="memo-card card-pink">
        <div class="card-top">
          <span class="card-id">PRM-07</span>
          <span class="badge badge-pink">SCHEDULER</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Cron</code></h3>
        <p class="card-desc">Scheduled handlers checked at compile time against Cloud Scheduler's grammar. Turn on <code>Singleflight</code> and overlapping runs collapse to one across all instances.</p>
        <div class="card-tags">
          <span class="tag">Cloud Scheduler</span>
          <span class="tag">Singleflight</span>
          <span class="tag">Timezones</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: In-process ticker</span>
          <span>Cloud: Cloud Scheduler</span>
        </div>
      </a>

      <a href="/reference/singleton" class="memo-card card-amber">
        <div class="card-top">
          <span class="card-id">PRM-08</span>
          <span class="badge badge-amber">COORDINATION</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>Singleton</code> &amp; <code>Lock</code></h3>
        <p class="card-desc">Distributed 30s leases with fencing tokens, and <code>Singleton</code> loops guaranteed to run on at most one instance — even mid-deploy while two revisions are live.</p>
        <div class="card-tags">
          <span class="tag">Fenced Leases</span>
          <span class="tag">Exactly-One</span>
          <span class="tag">CAS</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: Lease table</span>
          <span>Cloud: Datastore CAS</span>
        </div>
      </a>

      <a href="/reference/job" class="memo-card card-blue">
        <div class="card-top">
          <span class="card-id">PRM-09</span>
          <span class="badge badge-blue">BATCH COMPUTE</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Job[T]</code> + <code>Runner</code></h3>
        <p class="card-desc">Work that outlives request timeouts. Hands off to a Cloud Run Job with optional parallel task sharding — <code>ctx.Task()</code> gives each shard its index and count.</p>
        <div class="card-tags">
          <span class="tag">Cloud Run Jobs</span>
          <span class="tag">Parallel Shards</span>
          <span class="tag">No Timeout</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: Joined goroutines</span>
          <span>Cloud: Cloud Run Job</span>
        </div>
      </a>

      <a href="/reference/observability" class="memo-card card-cyan">
        <div class="card-top">
          <span class="card-id">PRM-10</span>
          <span class="badge badge-cyan">OBSERVABILITY</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>Secret</code>, <code>Counter</code> &amp; <code>Logs</code></h3>
        <p class="card-desc">Secrets resolve at boot — if one is unset, the binary refuses to start, so <code>Value()</code> never fails mid-request. Metrics never block; logs carry <code>req</code>, <code>job</code>, or <code>cron</code> IDs.</p>
        <div class="card-tags">
          <span class="tag">Secret Manager</span>
          <span class="tag">Monitoring</span>
          <span class="tag">slog</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: .env &amp; console</span>
          <span>Cloud: Secret Mgr &amp; GCM</span>
        </div>
      </a>

      <a href="/reference/alerts" class="memo-card card-pink">
        <div class="card-top">
          <span class="card-id">PRM-11</span>
          <span class="badge badge-pink">ALERTING</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.Alert</code></h3>
        <p class="card-desc">Paging conditions (<code>QueueBacklogged</code>, <code>CronMissed</code>, <code>RequestsErroring</code>, <code>SingletonDown</code>) declared in Go. An alert nobody receives does not compile.</p>
        <div class="card-tags">
          <span class="tag">PromQL</span>
          <span class="tag">On-Call</span>
          <span class="tag">tako alerts</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: tako alerts CLI</span>
          <span>Cloud: Monitoring Policy</span>
        </div>
      </a>

      <a href="/reference/testing" class="memo-card card-purple">
        <div class="card-top">
          <span class="card-id">PRM-12</span>
          <span class="badge badge-purple">TESTING &amp; CHAOS</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>tako.NewTestApp</code></h3>
        <p class="card-desc">Boot the whole app in <code>go test</code> with fresh in-memory state. Sign in test users, drain queues, advance a fake clock past TTLs, and turn on chaos mode to test delivery order.</p>
        <div class="card-tags">
          <span class="tag">Fake Clock</span>
          <span class="tag">Chaos Mode</span>
          <span class="tag">Zero Docker</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: go test / tako test</span>
          <span>Cloud: Conformance suite</span>
        </div>
      </a>

      <a href="/reference/cfire" class="memo-card card-blue">
        <div class="card-top">
          <span class="card-id">PRM-13</span>
          <span class="badge badge-blue">C++23 &amp; CFIRE</span>
          <span class="card-status">LOCKED</span>
        </div>
        <h3 class="card-title"><code>import "core:tako"</code></h3>
        <p class="card-desc">Write handlers and resources in native <code>cfire</code> (<code>.cf</code>). Analyzed into the same <code>AppGraph</code> and linked against <code>libtako.a</code> onto distroless C++23 containers.</p>
        <div class="card-tags">
          <span class="tag">cfire</span>
          <span class="tag">libtako.a</span>
          <span class="tag">distroless/cc</span>
        </div>
        <div class="card-footer-meta">
          <span>Local: cfire + libtako.a</span>
          <span>Cloud: Cloud Run C++23</span>
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
        <h3>One Process. No Emulators. No Docker.</h3>
        <p>
          <code>tako dev</code> runs your whole application graph in one ordinary Go process. Queues and topics are channels calling your functions, so a breakpoint steps straight from publish into the worker. Delete <code>.tako/data</code> and you're back to a clean slate.
        </p>
        <div class="panel-stat-row">
          <div class="p-stat"><strong>1</strong>single process</div>
          <div class="p-stat"><strong>0</strong>emulators needed</div>
          <div class="p-stat"><strong>.tako/data</strong>one-command reset</div>
        </div>
        <span class="panel-link">Read the local story →</span>
      </a>

      <a href="/guide/cloud" class="story-panel cloud-panel">
        <div class="panel-header">
          <span class="pill-dot dot-blue"></span>
          <span class="panel-tag">THE CLOUD STORY</span>
        </div>
        <h3>No State File. Derived Least-Privilege IAM.</h3>
        <p>
          <code>tako deploy</code> reads the live GCP project, diffs it against your code, and prints a classified plan before touching anything. Each service gets its own service account, granted only the resources its handlers actually touch. Delete the code and the next deploy revokes the grant.
        </p>
        <div class="panel-stat-row">
          <div class="p-stat"><strong>0</strong>YAML / Terraform</div>
          <div class="p-stat"><strong>100%</strong>code-derived IAM</div>
          <div class="p-stat"><strong>Stateless</strong>convergence</div>
        </div>
        <span class="panel-link">Read the cloud story →</span>
      </a>
    </div>
  </section>

  <section class="memo-section">
    <div class="memo-section-header">// 0x03 Worked Examples</div>
    <h2 class="memo-section-title">Seven Progressive Chapters</h2>
    <p class="memo-section-sub">
      Start with the mental model, then walk seven working apps in the order the framework grows. Every example is both a chapter in the guide and a test target in the conformance suite.
    </p>

    <div class="tutorial-list">
      <a href="/guide/00-mental-model" class="tutorial-item">
        <div class="t-num">00</div>
        <div class="t-body">
          <div class="t-title">mental model — How Tako Compiles Your Code</div>
          <div class="t-desc">Static analysis rules, package-as-service partitioning, variable-as-identity, request/error matrices, and the .tako/ directory.</div>
        </div>
        <div class="t-tags"><span class="tag">Architecture</span><span class="tag">Rules</span></div>
      </a>

      <a href="/guide/01-hello" class="tutorial-item">
        <div class="t-num">01</div>
        <div class="t-body">
          <div class="t-title">hello — APIs, Request Binding, &amp; First Deploy</div>
          <div class="t-desc">A 20-line service: path and query tags, hot reload with tako dev, and your first deploy to Cloud Run.</div>
        </div>
        <div class="t-tags"><span class="tag">Endpoints</span><span class="tag">Binding</span></div>
      </a>

      <a href="/guide/02-shortlink" class="tutorial-item">
        <div class="t-num">02</div>
        <div class="t-body">
          <div class="t-title">shortlink — Entities, Transactions, &amp; Queues</div>
          <div class="t-desc">Transactional writes, background click counting over a queue, and proving idempotency under chaos delivery.</div>
        </div>
        <div class="t-tags"><span class="tag">Datastore</span><span class="tag">Cloud Tasks</span></div>
      </a>

      <a href="/guide/03-gallery" class="tutorial-item">
        <div class="t-num">03</div>
        <div class="t-body">
          <div class="t-title">gallery — Buckets, Signed URLs, Fan-Out, &amp; Crons</div>
          <div class="t-desc">Direct-to-bucket uploads, Pub/Sub thumbnail workers, dead-letter replay, scheduled cleanup, secrets, and locks.</div>
        </div>
        <div class="t-tags"><span class="tag">GCS</span><span class="tag">Pub/Sub</span><span class="tag">Cron</span></div>
      </a>

      <a href="/guide/04-journal" class="tutorial-item">
        <div class="t-num">04</div>
        <div class="t-body">
          <div class="t-title">journal — Server-Rendered Pages, HTMX, &amp; Google Sign-In</div>
          <div class="t-desc">HTML templates, CSRF-checked forms, HTMX partial responses, sanitized markdown, and Google auth with the local dev bar.</div>
        </div>
        <div class="t-tags"><span class="tag">HTMX</span><span class="tag">Auth</span></div>
      </a>

      <a href="/guide/05-shop" class="tutorial-item">
        <div class="t-num">05</div>
        <div class="t-body">
          <div class="t-title">shop — Multi-Service RPC, TypeScript Client, &amp; Jobs</div>
          <div class="t-desc">Package = service: private Cloud Run services, typed cross-service calls, a generated TypeScript client, batch jobs, and a singleton invoicer.</div>
        </div>
        <div class="t-tags"><span class="tag">RPC</span><span class="tag">Jobs</span><span class="tag">Singleton</span></div>
      </a>

      <a href="/guide/06-blog" class="tutorial-item">
        <div class="t-num">06</div>
        <div class="t-body">
          <div class="t-title">blog — Hugo Static Site with Dynamic HTMX Islands</div>
          <div class="t-desc">Hugo builds the static pages while Tako runs the build loop and serves live comment fragments from the same origin.</div>
        </div>
        <div class="t-tags"><span class="tag">Hugo</span><span class="tag">Islands</span></div>
      </a>

      <a href="/guide/07-cfire" class="tutorial-item">
        <div class="t-num">07</div>
        <div class="t-body">
          <div class="t-title">cfire — Native C++23 Systems Apps on Tako</div>
          <div class="t-desc">Write .cf systems packages using import "core:tako", linked via the libtako C-archive bridge into distroless C++23 containers.</div>
        </div>
        <div class="t-tags"><span class="tag">C++23</span><span class="tag">cfire</span><span class="tag">libtako</span></div>
      </a>
    </div>
  </section>
</div>
{{template "foot" .}}{{end}}

{{define "api"}}{{template "head" .}}
<div class="doc-meta-bar">
  <div class="doc-breadcrumb"><span>api</span> / surface.txt</div>
  <button type="button" class="doc-cli-pill code-copy-btn" data-copy="tako docs api" title="Copy CLI doc lookup command">
    <span class="prompt">$</span> <span>tako docs api</span>
  </button>
</div>
<h1>API</h1>
<p class="lede">Every exported symbol of <code>github.com/gsandoval/tako</code>, from the
same lock file the API-freeze test compares against — so this page cannot quietly
lose one. Everything under <code>pkg/…</code> is tako's internals and is not here.</p>
<div class="api-filter-bar">
  <input type="search" id="api-symbol-filter" class="api-filter-input" placeholder="Filter exported symbols or signatures (e.g. Entity, Queue, Cron)..." aria-label="Filter API symbols">
</div>
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
	Name    string
	File    string
	Route   string
	CLIHint string
	Nav     []navItem
	TOC     []TOCItem
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
