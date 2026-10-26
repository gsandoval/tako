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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if isLocalBackend() {
			// No script on this page at all, so the policy can say so.
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'none'; object-src 'none'; base-uri 'none'")
			fmt.Fprintf(w, signInDevPage, html.EscapeString(next), devRoleChecks(), devPasswordForm(prov, next))
			return
		}
		apiKey, authDomain, project := os.Getenv("TAKO_AUTH_APIKEY"), os.Getenv("TAKO_AUTH_DOMAIN"), os.Getenv("TAKO_PROJECT")
		if apiKey == "" {
			http.Error(w, "sign-in is not configured — tako deploy injects the auth client config, so this is a tako bug; re-run tako deploy", http.StatusInternalServerError)
			return
		}
		google, email := "", ""
		if prov["google"] {
			google = `<button id="g" style="font:16px sans-serif;padding:10px 18px">Sign in with Google</button>`
		}
		if prov["email"] {
			email = `<form id="e" style="display:flex;flex-direction:column;gap:8px;max-width:280px;margin-top:16px">
  <input name="email" type="email" placeholder="email" required>
  <input name="password" type="password" placeholder="password" required>
  <button>Sign in with email</button></form>`
		}
		// A nonce, minted per response, so the page's own script runs and
		// an injected one does not. The policy names script and nothing
		// else on purpose — see the note on signInCloudPage.
		nonce := randomHex(16)
		w.Header().Set("Content-Security-Policy",
			"script-src 'self' https://www.gstatic.com 'nonce-"+nonce+"'; object-src 'none'; base-uri 'none'")
		fmt.Fprintf(w, signInCloudPage, google, email, nonce, apiKey, authDomain, project, jsString(next))
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

const signInDevPage = `<!doctype html><meta charset="utf-8"><title>sign in · tako dev</title>
<body style="font:16px sans-serif;max-width:32rem;margin:4rem auto">
<h1>Sign in (dev)</h1>
<p>Local sessions are dev-minted — no GCP involved. Pick a user:</p>
<form method="post" action="` + routes.Session + `"><input type="hidden" name="next" value="%s"><input type="hidden" name="dev_roles_form" value="1">
%s<button name="dev_user" value="alice">alice</button>
<button name="dev_user" value="bob">bob</button>
</form>%s</body>`

// devPasswordForm is the local half of the email/password door: the same
// exchange Identity Platform runs in the cloud, against the account
// store tako owns here. Rendered only when the app declared the
// provider, because a form for a door that does not exist is a lie.
func devPasswordForm(prov map[string]bool, next string) string {
	if !prov["email"] {
		return ""
	}
	return `<hr style="margin:24px 0"><p>Or with an account (` + "`auth.Users().Create`" + `):</p>
<form method="post" action="` + routes.Session + `" style="display:flex;flex-direction:column;gap:8px;max-width:280px">
<input type="hidden" name="next" value="` + html.EscapeString(next) + `">
<input name="email" type="email" placeholder="email" required>
<input name="password" type="password" placeholder="password" required>
<button>Sign in</button></form>`
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
	b.WriteString(`<fieldset style="border:1px solid #ccc;margin:0 0 16px"><legend>roles</legend>`)
	for _, role := range declaredRoles {
		fmt.Fprintf(&b, `<label style="margin-right:12px"><input type="checkbox" name="dev_role" value="%s"> %s</label>`,
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
const signInCloudPage = `<!doctype html><meta charset="utf-8"><title>sign in</title>
<body style="font:16px sans-serif;max-width:32rem;margin:4rem auto">
<h1>Sign in</h1>
%s%s
<p id="err" style="color:#b00"></p>
<script src="https://www.gstatic.com/firebasejs/10.14.1/firebase-app-compat.js"></script>
<script src="https://www.gstatic.com/firebasejs/10.14.1/firebase-auth-compat.js"></script>
<script nonce="%s">
firebase.initializeApp({apiKey: %q, authDomain: %q, projectId: %q});
const next = %s;
async function establish(cred) {
  const idToken = await cred.user.getIdToken();
  const res = await fetch("` + routes.Session + `", {method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({idToken})});
  if (!res.ok) throw new Error("session exchange failed: " + await res.text());
  location.assign(next);
}
const g = document.getElementById("g");
if (g) g.onclick = () =>
  firebase.auth().signInWithPopup(new firebase.auth.GoogleAuthProvider())
    .then(establish).catch(e => err.textContent = e.message);
const e = document.getElementById("e");
if (e) e.onsubmit = ev => { ev.preventDefault();
  firebase.auth().signInWithEmailAndPassword(e.email.value, e.password.value)
    .then(establish).catch(x => err.textContent = x.message); };
</script></body>`
