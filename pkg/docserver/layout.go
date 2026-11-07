package docserver

import (
	"html/template"
)

// One stylesheet, inlined. Works offline on an airplane with system fonts,
// with optional web fonts when online. Authored mobile-first (base rules target
// 320px–430px viewports with strict overflow containment) and progressively
// enhanced via @media (min-width: ...) breakpoints. Supports both Technical
// Paper Light Mode (:root / [data-theme="light"]) and Obsidian Dark Mode
// ([data-theme="dark"]).
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

/* ============================================================================
   1. MOBILE-FIRST BASE RESET & STRICT VIEWPORT CONTAINMENT (320px+)
   ============================================================================ */
*, *::before, *::after { box-sizing: border-box; }
::selection { background: var(--primary); color: #ffffff; }

html, body {
  margin: 0;
  padding: 0;
  width: 100%;
  max-width: 100%;
  overflow-x: hidden;
  background: var(--bg);
  color: var(--ink);
  font: 15px/1.62 var(--font-sans);
  -webkit-font-smoothing: antialiased;
  -webkit-text-size-adjust: 100%;
}

/* ============================================================================
   2. TWO-TIER STICKY MOBILE HEADER (Collapses to 1 Row at min-width: 900px)
   ============================================================================ */
header.top-nav {
  position: sticky;
  top: 0;
  z-index: 60;
  background: var(--bg);
  border-bottom: 1px solid var(--rule);
  display: flex;
  flex-direction: column;
  width: 100%;
}

.header-top-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  height: 2.9rem;
  padding: 0 0.85rem;
  border-bottom: 1px solid var(--rule);
  min-width: 0;
}

header .brand-link {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  text-decoration: none !important;
  color: var(--ink) !important;
  font-weight: 800;
  font-family: var(--font-mono);
  font-size: 1.02rem;
  letter-spacing: -0.03em;
  flex-shrink: 0;
}
header .brand-kamon {
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  display: block;
}
header .hanko-stamp {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-family: var(--font-serif-jp);
  font-size: 0.68rem;
  font-weight: 700;
  line-height: 1;
  color: var(--primary);
  background: var(--primary-bg);
  border: 1px solid var(--primary-border);
  padding: 0.16rem 0.28rem;
  border-radius: 3px;
}
header .brand-docs-sub {
  display: none;
  font-family: var(--font-mono);
  font-size: 0.72rem;
  font-weight: 600;
  color: var(--tertiary);
  border-left: 1px solid var(--rule);
  padding-left: 0.55rem;
}

header .header-right {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  flex-shrink: 0;
}

/* Segmented Controls (Go / cfire & Light / Dark) */
.code-tabs-switch, .header-lang-switch, .header-theme-switch {
  display: inline-flex;
  align-items: center;
  background: var(--surface-alt);
  border: 1px solid var(--rule);
  padding: 2px;
  border-radius: 4px;
  gap: 2px;
  flex-shrink: 0;
}
.code-tab-btn, .header-lang-btn, .header-theme-btn {
  border: 0;
  background: transparent;
  color: var(--dim);
  font: 600 0.72rem/1 var(--font-mono);
  padding: 0.28rem 0.48rem;
  border-radius: 3px;
  cursor: pointer;
  transition: all 0.12s ease;
  display: inline-flex;
  align-items: center;
  gap: 0.22rem;
  white-space: nowrap;
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
/* Compact icon-first theme buttons on mobile; full text label on desktop */
.header-theme-btn .theme-lbl {
  display: none;
}

header .github-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.38rem;
  color: var(--dim);
  font-size: 0.76rem;
  font-weight: 600;
  text-decoration: none !important;
  padding: 0.3rem 0.45rem;
  border-radius: 4px;
  border: 1px solid var(--rule);
  background: var(--surface);
  transition: all 0.12s ease;
  font-family: var(--font-mono);
  flex-shrink: 0;
}
header .github-link:hover {
  color: var(--ink);
  border-color: var(--rule-dark);
  background: var(--surface-hover);
}
header .github-link svg { flex-shrink: 0; }
header .github-link .github-lbl { display: none; }

/* Row 2 on Mobile: 4-Column Full-Width Section Tab Bar */
header .nav-links {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  width: 100%;
  height: 2.3rem;
  background: var(--surface);
}
header .nav-links a {
  color: var(--dim);
  text-decoration: none;
  font-family: var(--font-mono);
  font-size: 0.76rem;
  font-weight: 600;
  height: 100%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-bottom: 2px solid transparent;
  border-right: 1px solid var(--rule);
  transition: color 0.12s ease, border-color 0.12s ease, background-color 0.12s ease;
  white-space: nowrap;
}
header .nav-links a:last-child {
  border-right: 0;
}
header .nav-links a:hover {
  color: var(--ink);
  text-decoration: none;
}
header .nav-links a.on {
  color: var(--ink);
  font-weight: 700;
  border-bottom-color: var(--primary);
  background: var(--primary-bg);
}

/* ============================================================================
   3. MOBILE READER SUB-HEADER & COLLAPSIBLE 2-COL GROUPED DRAWER
   ============================================================================ */
.layout {
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 88rem;
  margin: 0 auto;
  padding: 0 0.9rem;
  min-width: 0;
}
.layout.layout-home {
  max-width: 78rem;
}
.layout.layout-home nav.side,
.layout.layout-home aside.toc,
.layout.layout-home .mobile-doc-bar {
  display: none !important;
}

/* Sticky Sub-Header Bar on Doc Pages (< 900px) */
.mobile-doc-bar {
  position: sticky;
  top: 5.2rem;
  z-index: 50;
  background: var(--bg);
  border-bottom: 1px solid var(--rule);
  margin: 0 -0.9rem;
  padding: 0.45rem 0.9rem;
  display: flex;
  align-items: center;
  gap: 0.45rem;
}

.mobile-nav-toggle {
  flex: 1 1 0;
  min-width: 0;
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.4rem;
  background: var(--surface);
  border: 1px solid var(--rule);
  border-radius: 4px;
  padding: 0.38rem 0.6rem;
  color: var(--ink);
  font: 600 0.75rem/1.2 var(--font-mono);
  cursor: pointer;
  text-align: left;
}
.mobile-nav-toggle:hover,
.mobile-nav-toggle[aria-expanded="true"] {
  border-color: var(--primary);
}
.mobile-nav-icon {
  color: var(--primary);
  font-size: 0.82rem;
  flex-shrink: 0;
}
.mobile-nav-label {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.mobile-nav-sec {
  color: var(--primary);
  font-weight: 700;
  text-transform: uppercase;
  margin-right: 0.2rem;
}
.mobile-nav-caret {
  color: var(--dim);
  font-size: 0.72rem;
  flex-shrink: 0;
  transition: transform 0.15s ease;
}
.mobile-nav-toggle[aria-expanded="true"] .mobile-nav-caret {
  transform: rotate(180deg);
}

.mobile-toc-select {
  flex: 0 1 44%;
  max-width: 44%;
  min-width: 7.5rem;
  background: var(--surface-alt);
  color: var(--ink);
  border: 1px solid var(--rule);
  border-radius: 4px;
  padding: 0.36rem 0.5rem;
  font: 600 0.73rem/1.2 var(--font-mono);
  cursor: pointer;
  outline: none;
  text-overflow: ellipsis;
}
.mobile-toc-select:focus {
  border-color: var(--primary);
}

/* Collapsible Grouped Topic Drawer on Mobile (< 900px) */
nav.side {
  display: none;
  width: 100%;
  background: var(--surface);
  border: 1px solid var(--rule-dark);
  border-radius: 6px;
  padding: 0.75rem;
  margin: 0.5rem 0 0.25rem;
  max-height: 68vh;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
}
nav.side.is-open {
  display: block;
}
.side-drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding-bottom: 0.5rem;
  margin-bottom: 0.35rem;
  border-bottom: 1px solid var(--rule);
  font: 700 0.68rem/1 var(--font-mono);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--tertiary);
}
.side-drawer-close {
  border: 1px solid var(--rule);
  background: var(--bg);
  color: var(--dim);
  border-radius: 3px;
  padding: 0.15rem 0.4rem;
  font: 600 0.72rem/1 var(--font-mono);
  cursor: pointer;
}
.side-nav-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.35rem;
}
nav.side .group {
  grid-column: 1 / -1;
  margin: 0.65rem 0 0.15rem;
  padding: 0 0.2rem 0.2rem;
  font-family: var(--font-mono);
  font-size: 0.68rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--primary);
  font-weight: 700;
  border-bottom: 1px solid var(--rule);
}
nav.side .group:first-child {
  margin-top: 0.1rem;
}
nav.side a {
  display: flex;
  align-items: center;
  padding: 0.38rem 0.55rem;
  color: var(--dim);
  text-decoration: none;
  font-family: var(--font-mono);
  font-size: 0.76rem;
  background: var(--bg);
  border: 1px solid var(--rule);
  border-radius: 4px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: all 0.12s ease;
}
nav.side a:hover {
  color: var(--ink);
  border-color: var(--rule-dark);
  text-decoration: none;
}
nav.side a.on {
  color: #ffffff;
  background: var(--primary);
  border-color: var(--primary);
  font-weight: 700;
}

main {
  min-width: 0;
  width: 100%;
  max-width: 100%;
  flex: 1;
  padding: 1.15rem 0 3.5rem;
}
.layout.layout-home main {
  padding-top: 0;
}

/* Right-Rail "On This Page" TOC (Hidden on Mobile/Tablet; Shown at min-width: 1180px) */
aside.toc {
  display: none;
}

/* ============================================================================
   4. UNIFIED GO / CFIRE CODE TABS & PROSE TYPOGRAPHY (Mobile-First)
   ============================================================================ */
.code-tabs {
  width: 100%;
  max-width: 54rem;
  min-width: 0;
  margin: 1rem 0;
  border: 1px solid var(--rule);
  border-radius: 6px;
  background: var(--code);
  overflow: hidden;
}
.code-tabs-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.45rem;
  padding: 0.35rem 0.6rem;
  background: var(--code-header);
  border-bottom: 1px solid var(--rule);
  flex-wrap: nowrap;
  min-width: 0;
}
.code-tabs-meta {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--dim);
  margin-left: auto;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}
.code-tab-caption { display: none; }
.code-tab-caption.on { display: inline; }
.code-tab-pane { display: none; min-width: 0; }
.code-tab-pane.on { display: block; }
.code-tab-pane pre {
  margin: 0;
  border: 0;
  border-radius: 0;
  max-width: 100%;
  background: var(--code);
}

.code-copy-btn {
  border: 1px solid var(--rule);
  background: var(--surface);
  color: var(--dim);
  font: 600 0.7rem/1 var(--font-mono);
  padding: 0.25rem 0.5rem;
  border-radius: 3px;
  cursor: pointer;
  transition: all 0.12s ease;
  flex-shrink: 0;
}
.code-copy-btn:hover {
  color: var(--ink);
  border-color: var(--rule-dark);
}
.code-copy-btn.copied {
  color: var(--accent-emerald);
  border-color: var(--accent-emerald-border);
}

/* Breadcrumb & CLI Topic Bar on Doc Pages */
.doc-meta-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  flex-wrap: wrap;
  margin-bottom: 0.85rem;
  padding-bottom: 0.6rem;
  border-bottom: 1px solid var(--rule);
}
.doc-breadcrumb {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--tertiary);
}
.doc-breadcrumb span { color: var(--primary); }
.doc-cli-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  background: var(--code);
  border: 1px solid var(--rule);
  border-radius: 4px;
  padding: 0.22rem 0.55rem;
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--dim);
  cursor: pointer;
  max-width: 100%;
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
  font-size: clamp(1.55rem, 4.5vw, 1.95rem);
  line-height: 1.2;
  letter-spacing: -0.03em;
  margin: 0.2rem 0 1rem;
  font-weight: 700;
  overflow-wrap: break-word;
}
h2 {
  font-size: clamp(1.18rem, 3.2vw, 1.35rem);
  letter-spacing: -0.02em;
  margin: 1.85rem 0 0.65rem;
  padding-bottom: 0.35rem;
  border-bottom: 1px solid var(--rule);
  font-weight: 650;
  overflow-wrap: break-word;
}
h3 {
  font-size: 1.04rem;
  letter-spacing: -0.01em;
  margin: 1.45rem 0 0.45rem;
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
  overflow-wrap: break-word;
}
ul, ol {
  padding-left: 1.35rem;
}
code {
  background: var(--surface-alt);
  border: 1px solid var(--rule);
  border-radius: 3px;
  padding: 0.1rem 0.3rem;
  font-size: 0.84em;
  font-family: var(--font-mono);
  overflow-wrap: break-word;
}
pre {
  background: var(--code);
  border: 1px solid var(--rule);
  border-radius: 6px;
  padding: 0.75rem 0.9rem;
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
  width: 100%;
  max-width: 54rem;
  margin: 0.9rem 0;
  position: relative;
}
pre code {
  background: none;
  border: 0;
  padding: 0;
  font-size: 0.78rem;
  line-height: 1.58;
  overflow-wrap: normal;
  word-break: normal;
}
pre.mermaid {
  text-align: center;
  padding: 1rem;
  background: var(--surface);
}
blockquote {
  margin: 1rem 0;
  padding: 0.65rem 0.9rem;
  background: var(--surface);
  border: 1px solid var(--rule);
  border-left: 3px solid var(--accent-blue);
  border-radius: 4px;
  color: var(--dim);
}
blockquote p { margin: 0.25rem 0; }

/* Mobile-First Scrollable Tables */
table {
  display: block;
  width: 100%;
  max-width: 54rem;
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
  border-collapse: collapse;
  margin: 1.1rem 0;
  font-size: 0.82rem;
  background: var(--surface);
  border: 1px solid var(--rule);
  border-radius: 6px;
}
th, td {
  border: 1px solid var(--rule);
  padding: 0.48rem 0.65rem;
  text-align: left;
  vertical-align: top;
}
th {
  background: var(--surface-alt);
  font-family: var(--font-mono);
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--dim);
  font-weight: 700;
  white-space: nowrap;
}
hr {
  border: 0;
  border-top: 1px solid var(--rule);
  margin: 1.75rem 0;
}
.repo { color: var(--dim); }
.repo code { font-size: 0.82em; }
.lede {
  color: var(--dim);
  font-size: 0.98rem;
  line-height: 1.62;
}

/* API Surface Lock List (Contained Horizontal Scroll on Mobile) */
.sym {
  list-style: none;
  padding: 0;
  margin: 0.5rem 0 1.4rem;
  border: 1px solid var(--rule);
  border-radius: 6px;
  background: var(--surface);
  overflow: hidden;
  width: 100%;
}
.sym li {
  margin: 0;
  padding: 0.42rem 0.75rem;
  border-bottom: 1px solid var(--rule);
  max-width: 100%;
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
  white-space: nowrap;
}
.sym li:last-child { border-bottom: 0; }
.sym .m {
  padding-left: 1.5rem;
  background: var(--code);
}
.sym code {
  background: none;
  border: 0;
  padding: 0;
  font-size: 0.78rem;
  white-space: nowrap;
}
.api-filter-bar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin: 0.9rem 0 1.35rem;
}
.api-filter-input {
  width: 100%;
  max-width: 28rem;
  background: var(--code);
  border: 1px solid var(--rule);
  border-radius: 4px;
  padding: 0.5rem 0.8rem;
  color: var(--ink);
  font: 0.84rem var(--font-mono);
}
.api-filter-input:focus {
  outline: none;
  border-color: var(--accent-blue);
}

footer {
  color: var(--dim);
  font-size: 0.78rem;
  border-top: 1px solid var(--rule);
  padding: 1.35rem 0;
  margin-top: 3rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 0.65rem;
}
footer .footer-brand {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  font-family: var(--font-mono);
  font-size: 0.75rem;
}

/* ============================================================================
   5. LANDING PAGE: MOBILE-FIRST HERO, CODE SYNTHESIS & CARDS (320px+)
   ============================================================================ */
.memo-container {
  max-width: 74rem;
  margin: 0 auto;
  width: 100%;
  min-width: 0;
}
.memo-section-header {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  font-weight: 700;
  text-transform: uppercase;
  color: var(--dim);
  letter-spacing: 0.07em;
  margin-bottom: 0.55rem;
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
}
.memo-hero {
  padding: 1.25rem 0 1.85rem;
  border-bottom: 1px solid var(--rule);
}
.hero-split-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 1.35rem;
  align-items: start;
}
.hero-left, .hero-right {
  min-width: 0;
  width: 100%;
}
.hero-eyebrow-row {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  flex-wrap: wrap;
  margin-bottom: 0.75rem;
}
.eyebrow-badge {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  font-family: var(--font-mono);
  font-size: 0.68rem;
  font-weight: 700;
  letter-spacing: 0.05em;
  padding: 0.22rem 0.5rem;
  border-radius: 4px;
  background: var(--surface);
  border: 1px solid var(--rule);
  color: var(--dim);
}
.eyebrow-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent-emerald);
  flex-shrink: 0;
}
.handwritten-sticker {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  font-weight: 700;
  color: var(--primary);
  background: var(--primary-bg);
  border: 1px solid var(--primary-border);
  padding: 0.2rem 0.5rem;
  border-radius: 4px;
  white-space: nowrap;
  display: inline-flex;
  align-items: center;
}
.memo-hero-title {
  font-size: clamp(1.85rem, 6vw, 3.1rem);
  font-weight: 800;
  letter-spacing: -0.04em;
  line-height: 1.12;
  margin: 0 0 0.85rem;
}
.flat-vermilion {
  color: var(--primary);
  border-bottom: 2px solid var(--primary);
}
.memo-hero-desc {
  font-size: 0.94rem;
  line-height: 1.58;
  color: var(--dim);
  max-width: 40rem;
  margin: 0 0 1.1rem;
}
.memo-hero-desc strong { color: var(--ink); }

/* Mobile-First 2-Column Action Grid (Keeps Install + Code Card Above the Fold) */
.memo-actions {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.5rem;
  margin-bottom: 1rem;
}
.m-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
  padding: 0.58rem 0.75rem;
  font-family: var(--font-mono);
  font-size: 0.76rem;
  font-weight: 600;
  border-radius: 4px;
  text-decoration: none !important;
  transition: all 0.12s ease;
  cursor: pointer;
  border: 1px solid transparent;
  white-space: nowrap;
  min-width: 0;
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
  background-color: var(--surface-alt);
  border-color: var(--rule);
  color: var(--accent-blue) !important;
  font-weight: 600;
}

.memo-install-box {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  background-color: var(--code);
  border: 1px solid var(--rule);
  padding: 0.48rem 0.65rem;
  border-radius: 6px;
  font-family: var(--font-mono);
  font-size: 0.76rem;
  width: 100%;
  max-width: 36rem;
  min-width: 0;
  margin-bottom: 0.8rem;
}
.terminal-bar {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  min-width: 0;
  flex: 1;
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
  scrollbar-width: none;
  white-space: nowrap;
}
.terminal-bar::-webkit-scrollbar { display: none; }
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
  font-size: 0.75rem;
  white-space: nowrap;
}
.hero-guarantees-strip {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.35rem 0.65rem;
  font-family: var(--font-mono);
  font-size: 0.72rem;
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
  width: 100%;
  min-width: 0;
}
.hero-synth-card .code-tabs {
  margin: 0;
  border: 0;
  border-radius: 0;
  max-width: none;
}
.hero-synth-card pre {
  padding: 0.75rem 0.85rem;
  font-size: 0.76rem;
}
.synth-drawer {
  border-top: 1px solid var(--rule);
  background: var(--surface);
  padding: 0.65rem 0.75rem;
  font-family: var(--font-mono);
  font-size: 0.72rem;
}
.synth-drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.4rem;
  color: var(--tertiary);
  font-size: 0.64rem;
  font-weight: 700;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  margin-bottom: 0.45rem;
}
.synth-drawer-header .status {
  color: var(--accent-emerald);
  flex-shrink: 0;
}
.synth-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.45rem;
}
.synth-row {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  padding: 0.45rem 0.55rem;
  border: 1px solid var(--rule);
  border-radius: 4px;
  background: var(--code);
  line-height: 1.4;
  min-width: 0;
}
.synth-row .lbl-local {
  color: var(--accent-emerald);
  font-weight: 700;
  font-size: 0.68rem;
}
.synth-row .lbl-cloud {
  color: var(--accent-blue);
  font-weight: 700;
  font-size: 0.68rem;
}
.synth-row .val {
  color: var(--dim);
  font-size: 0.7rem;
  overflow-wrap: break-word;
}

.memo-section {
  padding: 2rem 0;
  border-bottom: 1px solid var(--rule);
}
.memo-section-title {
  font-size: clamp(1.32rem, 4vw, 1.55rem);
  font-weight: 750;
  letter-spacing: -0.03em;
  color: var(--ink);
  margin: 0 0 0.35rem;
  border: none !important;
  padding: 0 !important;
}
.memo-section-sub {
  color: var(--dim);
  font-size: 0.9rem;
  max-width: 46rem;
  margin: 0 0 1.25rem;
  line-height: 1.58;
}

/* Mobile-First 1-Column Primitive Card Grid */
.memo-project-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 0.85rem;
}
.memo-card {
  background-color: var(--surface);
  border: 1px solid var(--rule);
  border-radius: 6px;
  padding: 1.05rem;
  text-decoration: none !important;
  color: inherit !important;
  display: flex;
  flex-direction: column;
  min-width: 0;
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
  gap: 0.45rem;
  margin-bottom: 0.55rem;
}
.card-id {
  font-family: var(--font-mono);
  font-size: 0.68rem;
  font-weight: 700;
  color: var(--tertiary);
  border: 1px solid var(--rule);
  background: var(--code);
  padding: 0.1rem 0.38rem;
  border-radius: 3px;
}
.badge {
  display: inline-block;
  padding: 0.14rem 0.44rem;
  font-size: 0.66rem;
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
  font-size: 0.64rem;
  color: var(--tertiary);
}
.card-title {
  margin: 0 0 0.35rem !important;
  font-size: 1.05rem !important;
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
  font-size: 0.84rem;
  line-height: 1.55;
  flex: 1;
  margin: 0 0 0.85rem;
}
.card-tags {
  display: flex;
  gap: 0.3rem;
  flex-wrap: wrap;
  margin-bottom: 0.75rem;
}
.tag {
  display: inline-block;
  padding: 0.12rem 0.38rem;
  font-size: 0.68rem;
  background-color: var(--code);
  color: var(--dim);
  border: 1px solid var(--rule);
  font-family: var(--font-mono);
  border-radius: 3px;
}
.card-footer-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.4rem;
  border-top: 1px solid var(--rule);
  padding-top: 0.55rem;
  font-family: var(--font-mono);
  font-size: 0.68rem;
  color: var(--tertiary);
}
.card-footer-meta span {
  background: var(--code);
  border: 1px solid var(--rule);
  border-radius: 3px;
  padding: 0.25rem 0.45rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.card-footer-meta span:first-child { color: var(--accent-emerald); }
.card-footer-meta span:last-child { color: var(--accent-blue); }

/* Mobile-First Two Stories & Compact 3-Col Stat Row */
.two-stories-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 0.9rem;
}
.story-panel {
  border-radius: 6px;
  padding: 1.15rem;
  text-decoration: none !important;
  color: inherit !important;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--rule);
  min-width: 0;
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
  gap: 0.45rem;
  margin-bottom: 0.5rem;
}
.pill-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.dot-emerald { background-color: var(--accent-emerald); }
.dot-blue { background-color: var(--accent-blue); }
.panel-tag {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  font-weight: 700;
  letter-spacing: 0.06em;
  color: var(--dim);
}
.story-panel h3 {
  font-size: 1.15rem;
  font-weight: 700;
  margin: 0 0 0.5rem;
  border: none !important;
  padding: 0 !important;
  color: var(--ink);
}
.story-panel p {
  color: var(--dim);
  font-size: 0.85rem;
  line-height: 1.58;
  flex: 1;
  margin: 0 0 0.95rem;
}
.panel-stat-row {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.4rem;
  margin-bottom: 0.85rem;
}
.p-stat {
  font-family: var(--font-mono);
  font-size: 0.66rem;
  color: var(--dim);
  background: var(--surface);
  border: 1px solid var(--rule);
  border-radius: 4px;
  padding: 0.4rem 0.45rem;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.p-stat strong {
  display: block;
  color: var(--ink);
  font-size: 0.82rem;
  margin-bottom: 0.08rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.panel-link {
  font-family: var(--font-mono);
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--accent-blue);
}

/* Mobile-First Worked Examples List */
.tutorial-list {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.tutorial-item {
  background-color: var(--surface);
  border: 1px solid var(--rule);
  border-radius: 6px;
  padding: 0.8rem 0.9rem;
  display: flex;
  align-items: flex-start;
  gap: 0.75rem;
  text-decoration: none !important;
  color: inherit !important;
  min-width: 0;
  transition: border-color 0.12s ease, background-color 0.12s ease;
}
.tutorial-item:hover {
  border-color: var(--primary);
  background-color: var(--surface-hover);
}
.t-num {
  font-family: var(--font-mono);
  font-size: 0.78rem;
  font-weight: 700;
  color: var(--primary);
  background: var(--primary-bg);
  border: 1px solid var(--primary-border);
  width: 1.85rem;
  height: 1.85rem;
  border-radius: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin-top: 0.1rem;
}
.t-body { flex: 1; min-width: 0; }
.t-title {
  font-family: var(--font-mono);
  font-weight: 650;
  font-size: 0.86rem;
  color: var(--ink);
  margin-bottom: 0.2rem;
  line-height: 1.35;
}
.t-desc {
  font-size: 0.8rem;
  color: var(--dim);
  line-height: 1.45;
}
.t-tags {
  display: none;
}

/* ============================================================================
   6. PROGRESSIVE ENHANCEMENT BREAKPOINTS (min-width: 640px / 900px / 1180px)
   ============================================================================ */

/* Tablet Small (640px+): 2-Column Grids, Inline CTA Row, Normal Tables */
@media (min-width: 640px) {
  .layout { padding: 0 1.25rem; }
  .mobile-doc-bar { margin: 0 -1.25rem; padding: 0.5rem 1.25rem; }
  .memo-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.65rem;
    margin-bottom: 1.25rem;
  }
  .m-btn { padding: 0.6rem 1.1rem; font-size: 0.8rem; }
  .m-btn-link { background: transparent; border-color: transparent; padding: 0.6rem 0.4rem; }
  .hero-guarantees-strip {
    display: flex;
    flex-wrap: wrap;
    gap: 0.85rem;
  }
  .memo-project-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1rem;
  }
  .two-stories-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1.1rem;
  }
  .tutorial-item {
    align-items: center;
    padding: 0.85rem 1.1rem;
  }
  .t-tags {
    display: flex;
    gap: 0.35rem;
    flex-shrink: 0;
  }
  table { display: table; font-size: 0.86rem; }
}

/* Desktop & Landscape Tablet (900px+): Single-Row Header, Sticky Left Sidebar, Split Hero */
@media (min-width: 900px) {
  header.top-nav {
    flex-direction: row;
    align-items: center;
    height: 3.5rem;
    padding: 0 1.5rem;
    gap: 1.25rem;
  }
  .header-top-row {
    display: contents;
  }
  header .brand-link {
    order: 0;
    font-size: 1.08rem;
    gap: 0.55rem;
  }
  header .brand-kamon { width: 28px; height: 28px; }
  header .brand-docs-sub { display: inline; }
  header .nav-links {
    order: 1;
    display: flex;
    align-items: center;
    width: auto;
    height: 100%;
    background: transparent;
    gap: 0.25rem;
    margin-left: 0.25rem;
  }
  header .nav-links a {
    padding: 0 0.7rem;
    font-size: 0.82rem;
    border-right: 0;
    background: transparent !important;
  }
  header .header-right {
    order: 2;
    margin-left: auto;
    gap: 0.55rem;
  }
  .header-theme-btn .theme-lbl { display: inline; }
  header .github-link { padding: 0.28rem 0.6rem; }
  header .github-link .github-lbl { display: inline; }

  .mobile-doc-bar { display: none !important; }
  .side-drawer-header { display: none !important; }

  .layout {
    flex-direction: row;
    align-items: flex-start;
    gap: 2.25rem;
    padding: 0 1.5rem;
  }
  nav.side {
    display: block !important;
    width: 15.5rem;
    flex: none;
    position: sticky;
    top: 3.5rem;
    margin: 0;
    padding: 1.5rem 0.75rem 2.5rem 0;
    max-height: calc(100vh - 3.5rem);
    overflow-y: auto;
    background: transparent;
    border: 0;
    border-right: 1px solid var(--rule);
    border-radius: 0;
  }
  .side-nav-grid {
    display: block;
  }
  nav.side .group {
    margin: 1.15rem 0 0.35rem;
    padding: 0 0.65rem;
    color: var(--tertiary);
    border-bottom: 0;
  }
  nav.side .group:first-child { margin-top: 0.2rem; }
  nav.side a {
    display: block;
    padding: 0.28rem 0.65rem;
    margin: 1px 0;
    font-size: 0.81rem;
    background: transparent;
    border: 0;
    border-left: 2px solid transparent;
    border-radius: 0 4px 4px 0;
  }
  nav.side a:hover {
    color: var(--ink);
    background: var(--surface-hover);
  }
  nav.side a.on {
    color: var(--primary);
    background: var(--primary-bg);
    border-left-color: var(--primary);
  }

  main {
    padding: 1.75rem 0 5rem;
    max-width: 52rem;
  }
  .layout.layout-home main {
    max-width: none;
    padding-top: 0;
  }

  .memo-hero { padding: 2.6rem 0 2.9rem; }
  .hero-split-grid {
    grid-template-columns: minmax(0, 1.08fr) minmax(0, 0.92fr);
    gap: 2.25rem;
  }
  .memo-section { padding: 2.65rem 0; }
}

/* Wide Desktop (1024px+): 3-Column Primitive Card Grid */
@media (min-width: 1024px) {
  .memo-project-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

/* Full 3-Column Documentation Chassis (1180px+): Sticky Right-Rail TOC */
@media (min-width: 1180px) {
  aside.toc {
    display: block;
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
  aside.toc .toc-list li { margin: 0.15rem 0; }
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
  <div class="header-top-row">
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
    <div class="header-right">
      <div class="header-lang-switch" role="tablist" aria-label="Preferred code language" title="Switch all code examples between Go and cfire">
        <button type="button" class="header-lang-btn on" data-lang="go" role="tab" aria-selected="true">Go</button>
        <button type="button" class="header-lang-btn" data-lang="cfire" role="tab" aria-selected="false">cfire</button>
      </div>
      <div class="header-theme-switch" role="group" aria-label="Color theme" title="Toggle between Technical Paper Light and Obsidian Dark theme">
        <button type="button" class="header-theme-btn" data-theme-btn="light" aria-label="Light theme">☀<span class="theme-lbl"> Light</span></button>
        <button type="button" class="header-theme-btn on" data-theme-btn="dark" aria-label="Dark theme">☾<span class="theme-lbl"> Dark</span></button>
      </div>
      <a href="https://github.com/gsandoval/tako" target="_blank" rel="noopener noreferrer" class="github-link" title="GitHub Repository">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
        <span class="github-lbl">GitHub</span>
      </a>
    </div>
  </div>
  <nav class="nav-links" aria-label="Primary sections">
    <a href="/guide/"{{if eq .Section "guide"}} class="on"{{end}}>guide</a>
    <a href="/reference/"{{if eq .Section "reference"}} class="on"{{end}}>reference</a>
    <a href="/api"{{if eq .Section "api"}} class="on"{{end}}>api</a>
    <a href="/llms.txt">llms.txt</a>
  </nav>
</header>
<div class="layout{{if eq .Section ""}} layout-home{{end}}">
{{if ne .Section ""}}
<div class="mobile-doc-bar">
  <button type="button" class="mobile-nav-toggle" id="mobile-nav-toggle" aria-expanded="false" aria-controls="doc-side-nav">
    <span class="mobile-nav-icon">☰</span>
    <span class="mobile-nav-label"><span class="mobile-nav-sec">{{.Section}}:</span> {{if .Name}}{{.Name}}{{else if .Title}}{{.Title}}{{else}}overview{{end}}</span>
    <span class="mobile-nav-caret">▾</span>
  </button>
  {{if .TOC}}
  <select class="mobile-toc-select" id="mobile-toc-select" aria-label="Jump to section on this page">
    <option value="">On this page ▾</option>
    {{range .TOC}}<option value="#{{.ID}}">{{if eq .Level 3}}↳ {{end}}{{.Title}}</option>{{end}}
  </select>
  {{else if eq .Section "api"}}
  <select class="mobile-toc-select" id="mobile-toc-select" aria-label="Jump to API group">
    <option value="">Jump to group ▾</option>
    {{range .Groups}}<option value="#{{.Anchor}}">{{.Section}}</option>{{end}}
  </select>
  {{end}}
</div>
{{end}}
<nav class="side" id="doc-side-nav" aria-label="Section navigation">
  <div class="side-drawer-header">
    <span>MODULE &amp; TOPIC INDEX ({{.Section}})</span>
    <button type="button" class="side-drawer-close" id="side-drawer-close" aria-label="Close navigation drawer">✕</button>
  </div>
  <div class="side-nav-grid">{{range .Nav}}{{if .Group}}<div class="group">{{.Title}}</div>{{else}}<a href="{{.Route}}"{{if .On}} class="on"{{end}}>{{.Title}}</a>{{end}}{{end}}</div>
</nav>
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

// Mobile Collapsible Topic Drawer & On-This-Page Jumper
const navToggle = document.getElementById('mobile-nav-toggle');
const sideNav = document.getElementById('doc-side-nav');
const drawerClose = document.getElementById('side-drawer-close');
const tocSelect = document.getElementById('mobile-toc-select');

function setDrawerOpen(open) {
  if (!navToggle || !sideNav) return;
  sideNav.classList.toggle('is-open', open);
  navToggle.setAttribute('aria-expanded', open ? 'true' : 'false');
}

if (navToggle && sideNav) {
  navToggle.addEventListener('click', () => {
    setDrawerOpen(!sideNav.classList.contains('is-open'));
  });
}
if (drawerClose) {
  drawerClose.addEventListener('click', () => setDrawerOpen(false));
}
if (tocSelect) {
  tocSelect.addEventListener('change', () => {
    if (tocSelect.value) {
      window.location.hash = tocSelect.value;
      tocSelect.selectedIndex = 0;
    }
  });
}

document.addEventListener('click', async (e) => {
  if (sideNav && sideNav.classList.contains('is-open')) {
    if (e.target.closest('nav.side a')) {
      setDrawerOpen(false);
    } else if (!e.target.closest('nav.side') && !e.target.closest('#mobile-nav-toggle')) {
      setDrawerOpen(false);
    }
  }
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
            ⚡ Get Started (5m)
          </a>
          <a href="/reference/" class="m-btn m-btn-secondary">Explore Reference</a>
          <a href="/guide/00-mental-model" class="m-btn m-btn-secondary">Mental Model</a>
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
            <div class="synth-grid">
              <div class="synth-row">
                <span class="lbl-local">▪ LOCAL (CLI)</span>
                <span class="val">1 process · embedded bbolt (.tako/data) · in-memory channels</span>
              </div>
              <div class="synth-row">
                <span class="lbl-cloud">▪ CLOUD TARGET</span>
                <span class="val">Cloud Run (hello) + GCP Datastore (links) + Least-Privilege IAM</span>
              </div>
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
          <div class="p-stat"><strong>.tako/data</strong>one-cmd reset</div>
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
