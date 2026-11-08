package runtime

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
	"os"
	"strings"

	"github.com/gsandoval/tako/internal/routes"
)

// SignInPage serves GET /_tako/signin — the framework-owned
// sign-in UI ("auth is one declaration": the app links here, tako owns
// the flow). Locally it lists dev users (no GCP anywhere near tako
// dev); in the cloud it runs Identity Platform's browser flow with the
// client config the deploy injected, POSTs the resulting ID token to
// /_tako/api/session, and returns to ?next= (default /).
func SignInPage(providers ...string) http.Handler {
	prov := map[string]bool{}
	for _, p := range providers {
		prov[p] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := safeNext(r.URL.Query().Get("next"))
		escapedNext := html.EscapeString(next)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if isLocalBackend() {
			// No script on this page at all, so the policy can say so.
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'none'; style-src 'unsafe-inline'; object-src 'none'; base-uri 'none'")
			fmt.Fprintf(w, signInDevPage, signInStyles, escapedNext, escapedNext, devRoleChecks(), devPasswordForm(prov, next))
			return
		}
		apiKey, authDomain, project := os.Getenv("TAKO_AUTH_APIKEY"), os.Getenv("TAKO_AUTH_DOMAIN"), os.Getenv("TAKO_PROJECT")
		if apiKey == "" {
			http.Error(w, "sign-in is not configured — tako deploy injects the auth client config, so this is a tako bug; re-run tako deploy", http.StatusInternalServerError)
			return
		}
		google, divider, email := "", "", ""
		if prov["google"] {
			google = `<button id="g" type="button" class="btn btn-google">` + googleLogoSVG + `<span>Continue with Google</span></button>`
		}
		if prov["google"] && prov["email"] {
			divider = `<div class="divider"><span>OR EMAIL CREDENTIALS</span></div>`
		}
		if prov["email"] {
			tabs := ""
			if showCloudSignUpTabs(r.Context()) {
				tabs = `  <div class="mode-tabs" role="tablist" aria-label="Account mode">
    <button type="button" id="mode-signin" class="mode-tab active" aria-selected="true">Sign in</button>
    <button type="button" id="mode-signup" class="mode-tab" aria-selected="false">Create account</button>
  </div>
`
			}
			email = `<div class="email-block">
` + tabs + `  <form id="e" class="auth-form">
    <label class="field-label">
      <span>EMAIL ADDRESS</span>
      <input name="email" type="email" autocomplete="email" placeholder="you@domain.com" required>
    </label>
    <label class="field-label">
      <span>PASSWORD</span>
      <input id="pw" name="password" type="password" autocomplete="current-password" placeholder="••••••••" required>
    </label>
    <button id="e-submit" type="submit" class="btn btn-primary">Sign in with email</button>
  </form>
</div>`
		}
		// A nonce, minted per response, so the page's own script runs and
		// an injected one does not. The policy names script and nothing
		// else on purpose — see the note on signInCloudPage.
		nonce := randomHex(16)
		w.Header().Set("Content-Security-Policy",
			"script-src 'self' https://www.gstatic.com https://apis.google.com https://www.google.com 'nonce-"+nonce+"'; object-src 'none'; base-uri 'none'")
		projectBadge := html.EscapeString(project)
		if projectBadge == "" {
			projectBadge = "TAKO // AUTH"
		}
		fmt.Fprintf(w, signInCloudPage, signInStyles, projectBadge, escapedNext, google, divider, email, nonce, apiKey, authDomain, project, jsString(next))
	})
}

// jsString renders a value as a JavaScript string literal, quotes and
// all.
//
// %q was the bug: it escapes what breaks a GO literal — quotes,
// backslashes — and "</script>" contains neither, so a value carrying
// one closed the script ELEMENT and everything after it was markup.
// JSEscapeString escapes '<' and '>' too, which is what makes the
// literal safe where it actually sits.
func jsString(s string) string {
	return `"` + template.JSEscapeString(s) + `"`
}

const googleLogoSVG = `<svg class="google-icon" viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><path fill="#4285F4" d="M23.49 12.27c0-.79-.07-1.54-.19-2.27H12v4.51h6.47c-.29 1.48-1.14 2.73-2.4 3.58v3h3.86c2.26-2.09 3.56-5.17 3.56-8.82z"/><path fill="#34A853" d="M12 24c3.24 0 5.95-1.08 7.93-2.91l-3.86-3c-1.08.72-2.45 1.16-4.07 1.16-3.13 0-5.78-2.11-6.73-4.96H1.29v3.09C3.26 21.3 7.31 24 12 24z"/><path fill="#FBBC05" d="M5.27 14.29c-.25-.72-.38-1.49-.38-2.29s.13-1.57.38-2.29V6.62H1.29C.47 8.24 0 10.06 0 12s.47 3.76 1.29 5.38l3.98-3.09z"/><path fill="#EA4335" d="M12 4.75c1.77 0 3.35.61 4.6 1.8l3.42-3.42C17.95 1.19 15.24 0 12 0 7.31 0 3.26 2.7 1.29 6.62l3.98 3.09c.95-2.85 3.6-4.96 6.73-4.96z"/></svg>`

const signInStyles = `<style>
:root {
  --bg: #F4F3EF;
  --surface: #FAF9F6;
  --surface-low: #EAE8E1;
  --surface-high: #E2E0D8;
  --ink: #141413;
  --ink-secondary: #52514C;
  --ink-muted: #787771;
  --hairline: #D4D2C9;
  --crimson: #BA1A1A;
  --crimson-bg: #FFDAD6;
  --emerald: #059669;
  --font-sans: "Geist", "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  --font-mono: "JetBrains Mono", "Geist Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
*, *::before, *::after {
  box-sizing: border-box;
  border-radius: 2px;
}
html, body {
  margin: 0;
  padding: 0;
  background-color: var(--bg);
  color: var(--ink);
  font-family: var(--font-sans);
  font-size: 15px;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}
body {
  min-height: 100vh;
  min-height: 100dvh;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  padding: 16px;
}
.auth-shell {
  width: 100%;
  max-width: 400px;
  background: var(--surface);
  border: 1px solid var(--hairline);
  box-shadow: 0 1px 2px rgba(20, 20, 19, 0.04);
}
.auth-topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 16px;
  background: var(--surface-low);
  border-bottom: 1px solid var(--hairline);
  font-family: var(--font-mono);
  font-size: 11px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--ink-secondary);
}
.auth-badge {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-weight: 700;
  color: var(--ink);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--emerald);
  flex-shrink: 0;
}
.back-link {
  color: var(--ink-secondary);
  text-decoration: none;
  font-weight: 600;
  padding: 4px 6px;
  margin: -4px -6px;
}
.back-link:hover {
  color: var(--ink);
  text-decoration: underline;
}
.auth-body {
  padding: 24px 20px;
}
@media (min-width: 440px) {
  .auth-body {
    padding: 28px 24px;
  }
}
.auth-kicker {
  font-family: var(--font-mono);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--ink-muted);
  margin: 0 0 4px;
}
.auth-title {
  font-size: 22px;
  font-weight: 700;
  letter-spacing: -0.02em;
  margin: 0 0 6px;
  color: var(--ink);
}
.auth-subtitle {
  font-size: 13px;
  color: var(--ink-secondary);
  margin: 0 0 20px;
}
.btn {
  appearance: none;
  width: 100%;
  min-height: 44px;
  padding: 10px 16px;
  font-family: var(--font-sans);
  font-size: 14px;
  font-weight: 600;
  line-height: 1.2;
  border: 1px solid var(--ink);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  transition: background-color 0.12s ease, color 0.12s ease, opacity 0.12s ease;
}
.btn:disabled {
  opacity: 0.55;
  cursor: wait;
}
.btn-primary {
  background: var(--ink);
  color: var(--bg);
}
.btn-primary:hover:not(:disabled) {
  background: #2b2b29;
}
.btn-google {
  background: #FFFFFF;
  color: var(--ink);
  border-color: var(--hairline);
}
.btn-google:hover:not(:disabled) {
  background: var(--surface-low);
  border-color: var(--ink-secondary);
}
.google-icon {
  flex-shrink: 0;
}
.divider {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 20px 0;
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--ink-muted);
}
.divider::before, .divider::after {
  content: "";
  flex: 1;
  height: 1px;
  background: var(--hairline);
}
.mode-tabs {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 4px;
  padding: 3px;
  background: var(--surface-low);
  border: 1px solid var(--hairline);
  margin-bottom: 16px;
}
.mode-tab {
  appearance: none;
  border: 1px solid transparent;
  background: transparent;
  color: var(--ink-secondary);
  font-family: var(--font-mono);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  min-height: 34px;
  cursor: pointer;
}
.mode-tab.active {
  background: var(--surface);
  color: var(--ink);
  border-color: var(--hairline);
  box-shadow: 0 1px 1px rgba(20, 20, 19, 0.04);
}
.auth-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin: 0;
}
.field-label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-family: var(--font-mono);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--ink-secondary);
}
.field-label input {
  width: 100%;
  min-height: 44px;
  padding: 10px 12px;
  font-family: var(--font-sans);
  font-size: 16px;
  color: var(--ink);
  background: #FFFFFF;
  border: 1px solid var(--hairline);
  outline: none;
}
.field-label input:focus {
  border-color: var(--ink);
}
.err-box {
  margin: 16px 0 0;
  padding: 10px 12px;
  background: var(--crimson-bg);
  border: 1px solid var(--crimson);
  color: var(--crimson);
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.4;
  word-break: break-word;
}
.err-box:empty {
  display: none;
}
.dev-users {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}
.role-fieldset {
  border: 1px solid var(--hairline);
  background: var(--surface-low);
  padding: 10px 12px;
  margin: 0 0 14px;
}
.role-fieldset legend {
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--ink-secondary);
  padding: 0 4px;
}
.role-check {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-right: 12px;
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--ink);
  cursor: pointer;
}
.auth-footer {
  padding: 10px 16px;
  border-top: 1px solid var(--hairline);
  background: var(--surface-low);
  font-family: var(--font-mono);
  font-size: 10px;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--ink-muted);
  display: flex;
  justify-content: space-between;
}
</style>`

const signInDevPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>sign in · tako dev</title>
%s
</head>
<body>
<main class="auth-shell">
  <header class="auth-topbar">
    <span class="auth-badge"><span class="status-dot"></span>TAKO // DEV AUTH</span>
    <a class="back-link" href="%s">← Return</a>
  </header>
  <div class="auth-body">
    <p class="auth-kicker">LOCAL SESSION HARNESS</p>
    <h1 class="auth-title">Sign in (dev)</h1>
    <p class="auth-subtitle">Local sessions are dev-minted — no GCP involved. Pick a user:</p>
    <form method="post" action="` + routes.Session + `" class="auth-form">
      <input type="hidden" name="next" value="%s">
      <input type="hidden" name="dev_roles_form" value="1">
      %s
      <div class="dev-users">
        <button class="btn btn-google" name="dev_user" value="alice">alice</button>
        <button class="btn btn-google" name="dev_user" value="bob">bob</button>
      </div>
    </form>
    %s
  </div>
  <footer class="auth-footer">
    <span>MODE: LOCAL_DEV</span>
    <span>TAKO AUTH</span>
  </footer>
</main>
</body>
</html>`

// devPasswordForm is the local half of the email/password door: the same
// exchange Identity Platform runs in the cloud, against the account
// store tako owns here. Rendered only when the app declared the
// provider, because a form for a door that does not exist is a lie.
func devPasswordForm(prov map[string]bool, next string) string {
	if !prov["email"] {
		return ""
	}
	return `<div class="divider"><span>OR WITH AN ACCOUNT (` + "`auth.Users().Create`" + `)</span></div>
<form method="post" action="` + routes.Session + `" class="auth-form">
  <input type="hidden" name="next" value="` + html.EscapeString(next) + `">
  <label class="field-label"><span>EMAIL ADDRESS</span><input name="email" type="email" placeholder="you@domain.com" required></label>
  <label class="field-label"><span>PASSWORD</span><input name="password" type="password" placeholder="••••••••" required></label>
  <button class="btn btn-primary">Sign in</button>
</form>`
}

// declaredRoles is the app's tako.Role set, installed by the generated
// main. Empty means the app declared none, and the dev page shows no
// role picker at all rather than an empty fieldset.
var declaredRoles []string

// RegisterRoles hands the runtime the roles this app declared, so the
// dev sign-in page can offer them. Generated mains call it; there is no
// cloud equivalent, because in the cloud a role is a claim Identity
// Platform holds.
func RegisterRoles(roles ...string) { declaredRoles = roles }

func devRoleChecks() string {
	if len(declaredRoles) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<fieldset class="role-fieldset"><legend>roles</legend>`)
	for _, role := range declaredRoles {
		fmt.Fprintf(&b, `<label class="role-check"><input type="checkbox" name="dev_role" value="%s"> %s</label>`,
			html.EscapeString(role), html.EscapeString(role))
	}
	b.WriteString(`</fieldset>`)
	return b.String()
}

// The cloud page runs the standard Identity Platform browser flow via
// the Firebase Auth compat SDK (gstatic). One POST of the resulting ID
// token establishes the server session; the SDK's client state is not
// used afterwards.
//
// Its CSP names script-src, object-src and base-uri, and deliberately
// stops there. A default-src would have to enumerate every host the
// Identity Platform SDK reaches — token exchange, the popup handler,
// whatever a future SDK version adds — and a policy that breaks sign-in
// is a worse outcome than the reflected script it was written to catch.
// Those three directives are what an injected script needs: somewhere to
// run from, a plugin to fall back to, and a base tag to retarget
// relative URLs. The rest of the page's traffic is the SDK's business.
const signInCloudPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in</title>
%s
</head>
<body>
<main class="auth-shell">
  <header class="auth-topbar">
    <span class="auth-badge"><span class="status-dot"></span>%s</span>
    <a class="back-link" href="%s">← Return</a>
  </header>
  <div class="auth-body">
    <p class="auth-kicker">IDENTITY VERIFICATION</p>
    <h1 class="auth-title" id="auth-heading">Sign in</h1>
    <p class="auth-subtitle" id="auth-sub">Authenticate to establish an encrypted session.</p>
    %s%s%s
    <p id="err" class="err-box" role="alert"></p>
  </div>
  <footer class="auth-footer">
    <span>IDENTITY PLATFORM</span>
    <span>TLS // SESSION</span>
  </footer>
</main>
<script src="https://www.gstatic.com/firebasejs/10.14.1/firebase-app-compat.js"></script>
<script src="https://www.gstatic.com/firebasejs/10.14.1/firebase-auth-compat.js"></script>
<script nonce="%s">
firebase.initializeApp({apiKey: %q, authDomain: %q, projectId: %q});
const next = %s;
const err = document.getElementById("err");
const g = document.getElementById("g");
const e = document.getElementById("e");
const eSubmit = document.getElementById("e-submit");
const pwInput = document.getElementById("pw");
const modeSignIn = document.getElementById("mode-signin");
const modeSignUp = document.getElementById("mode-signup");
const heading = document.getElementById("auth-heading");
let isSignUp = false;

function friendlyError(ex) {
  const code = (ex && ex.code) || "";
  switch (code) {
    case "auth/invalid-credential":
    case "auth/wrong-password":
    case "auth/user-not-found":
      return modeSignUp
        ? "Invalid email or password. If you don't have an account yet, switch to Create account."
        : "Invalid email or password. Accounts are provisioned by a vault administrator.";
    case "auth/email-already-in-use":
      return "An account with this email already exists. Switch to Sign in.";
    case "auth/weak-password":
      return "Password must be at least 6 characters long.";
    case "auth/invalid-email":
      return "Please enter a valid email address.";
    case "auth/popup-closed-by-user":
    case "auth/cancelled-popup-request":
      return "Google sign-in window was closed before completing.";
    case "auth/popup-blocked":
      return "Popup was blocked by the browser. Redirecting to Google sign-in…";
    case "auth/too-many-requests":
      return "Too many attempts. Please wait a moment and try again.";
    case "auth/network-request-failed":
      return "Network error while contacting the authentication service.";
    default:
      return (ex && ex.message) ? ex.message.replace(/^Firebase:\s*/i, "") : "Authentication failed.";
  }
}

function setBusy(busy, label) {
  if (g) g.disabled = busy;
  if (eSubmit) {
    eSubmit.disabled = busy;
    if (label) eSubmit.textContent = label;
    else eSubmit.textContent = isSignUp ? "Create account" : "Sign in with email";
  }
}

function setMode(signUp) {
  isSignUp = signUp;
  err.textContent = "";
  if (modeSignIn && modeSignUp) {
    modeSignIn.classList.toggle("active", !signUp);
    modeSignIn.setAttribute("aria-selected", String(!signUp));
    modeSignUp.classList.toggle("active", signUp);
    modeSignUp.setAttribute("aria-selected", String(signUp));
  }
  if (heading) heading.textContent = signUp ? "Create account" : "Sign in";
  if (pwInput) pwInput.setAttribute("autocomplete", signUp ? "new-password" : "current-password");
  if (eSubmit) eSubmit.textContent = signUp ? "Create account" : "Sign in with email";
}

if (modeSignIn && modeSignUp) {
  modeSignIn.onclick = () => setMode(false);
  modeSignUp.onclick = () => setMode(true);
}

async function establish(cred) {
  if (!cred || !cred.user) return;
  const idToken = await cred.user.getIdToken();
  const res = await fetch("` + routes.Session + `", {method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({idToken})});
  if (!res.ok) throw new Error("session exchange failed: " + await res.text());
  location.assign(next);
}

firebase.auth().getRedirectResult().then(cred => {
  if (cred && cred.user) return establish(cred);
}).catch(ex => { err.textContent = friendlyError(ex); });

if (g) g.onclick = () => {
  err.textContent = "";
  setBusy(true);
  const provider = new firebase.auth.GoogleAuthProvider();
  firebase.auth().signInWithPopup(provider)
    .then(establish)
    .catch(ex => {
      if (ex && ex.code === "auth/popup-blocked") {
        err.textContent = friendlyError(ex);
        firebase.auth().signInWithRedirect(provider);
        return;
      }
      setBusy(false);
      err.textContent = friendlyError(ex);
    });
};

if (e) e.onsubmit = ev => {
  ev.preventDefault();
  err.textContent = "";
  const emailVal = e.email.value.trim();
  const passVal = e.password.value;
  setBusy(true, isSignUp ? "Creating account…" : "Signing in…");
  const action = isSignUp
    ? firebase.auth().createUserWithEmailAndPassword(emailVal, passVal)
    : firebase.auth().signInWithEmailAndPassword(emailVal, passVal);
  action.then(establish).catch(ex => {
    setBusy(false);
    err.textContent = friendlyError(ex);
  });
};
</script>
</body>
</html>`
