package journal

// The moderation desk — the door, not the desk.
//
// This is the example that demands Route.Allow. Before it, journal's only
// authorization was ownership, which is a question about a POST and
// rightly lives in the handler that loaded one. "Who may see everyone
// else's drafts" is a question about the ROUTE, and the four lines it
// used to take at the top of every such handler are the thing a
// declaration replaces.

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gsandoval/tako"
)

// A moderator gets the desk, with somebody else's draft on it.
func TestTheDeskShowsEveryonesWork(t *testing.T) {
	srv := servePages(t)

	// bob writes and unpublishes, so there is a draft that is not the
	// moderator's own.
	bob := client(t, srv)
	bobToken := signIn(t, bob, srv.URL, "bob")
	id := writePosts(t, bob, srv.URL, bobToken, "Bob draft")[0]
	unpublish, err := bob.PostForm(srv.URL+"/p/unpublish", url.Values{"selected": {id}, "_csrf": {bobToken}})
	if err != nil {
		t.Fatal(err)
	}
	unpublish.Body.Close()

	alice := freshClient(t)
	signInAs(t, alice, srv.URL, "alice", Moderator)
	res, err := alice.Get(srv.URL + "/moderate")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/moderate = %d for a moderator: %s", res.StatusCode, body)
	}
	for _, want := range []string{"Bob draft", "draft"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the desk is missing %q: %s", want, body)
		}
	}
}

// The refusal is a PAGE, and it says the right thing.
//
// Signed in, holding no role — which since AB10a is the whole of it. No
// environment flip and no allowlist: the door wants a role, this session
// does not have one.
func TestAStrangerAtTheDeskGetsAPageThatSaysWhy(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	signIn(t, c, srv.URL, "alice") // no roles ticked

	res, err := c.Get(srv.URL + "/moderate")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("/moderate = %d for a stranger, want 403: %s", res.StatusCode, body)
	}
	// The heading proves Status.Policy reached the page: without it the
	// status map answers 403 with CSRF's words, and a reader who is
	// simply not a moderator is told their form expired.
	if !strings.Contains(string(body), "Not your desk") {
		t.Errorf("the refusal wears CSRF's heading: %s", body)
	}
	// The refusal names the role that would have worked. "Not
	// authorized" is the message that sends somebody to ask a question
	// nobody can answer.
	if !strings.Contains(string(body), "moderator") {
		t.Errorf("the refusal does not name the role: %s", body)
	}
	// It is still the app's page, chrome and all — the thing a status
	// page exists for.
	if !strings.Contains(string(body), "Back to the journal") {
		t.Errorf("the refusal is not the app's page: %s", body)
	}
	// And locally it says how to fix it. This is tako.Environment()'s
	// whole purpose: a development affordance, never a change to the
	// product.
	if !strings.Contains(string(body), "tick the role") {
		t.Errorf("the local refusal does not say how to grant yourself the role: %s", body)
	}
}

// The same refusal, judged as a deploy judges it: no hint, because in
// production there is no /_tako/signin checkbox to send anyone to.
func TestTheDevHintIsLocalOnly(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	signIn(t, c, srv.URL, "alice")

	t.Setenv("TAKO_BACKEND", "gcp") // as a deploy sets it
	res, err := c.Get(srv.URL + "/moderate")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("/moderate = %d, want 403", res.StatusCode)
	}
	if strings.Contains(string(body), "tick the role") {
		t.Errorf("a deployed refusal offers a local affordance: %s", body)
	}
	if !strings.Contains(string(body), "moderator") {
		t.Errorf("the deployed refusal stopped naming the role: %s", body)
	}
}

// The local way in is a checkbox, and it only exists because the app
// declared the role.
func TestTheDevSignInPageOffersTheDeclaredRoles(t *testing.T) {
	srv := servePages(t)
	res, err := client(t, srv).Get(srv.URL + "/_tako/signin")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `name="dev_role" value="moderator"`) {
		t.Errorf("the dev sign-in page does not offer the moderator role: %s", body)
	}
}

func TestTheDeskRefusesAnAnonymousVisitorBeforeTheRule(t *testing.T) {
	srv := servePages(t)
	res, err := client(t, srv).Get(srv.URL + "/moderate")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/moderate = %d with no session, want 401", res.StatusCode)
	}
	// Not the policy's refusal: nobody has been judged yet.
	if strings.Contains(string(body), "Not your desk") {
		t.Errorf("a missing session was blamed on the policy: %s", body)
	}
}

// The same rule, on the call path — which is what makes it a rule and
// not a middleware. A cross-package caller reaching Retract.Call gets
// the door, not the act.
func TestTheDoorHoldsOnTheCallPath(t *testing.T) {
	app := tako.NewTestApp(t)

	stranger := app.SignIn("sam") // signed in, no roles
	if _, err := Retract.Call(app.Ctx(stranger), RetractReq{ID: "anything"}); !tako.IsDenied(err) {
		t.Fatalf("a stranger's Call was not denied: %v", err)
	}
	// And the role is the whole difference: same call, same code path.
	mod := app.SignIn("ana", Moderator)
	if _, err := Retract.Call(app.Ctx(mod), RetractReq{ID: "anything"}); tako.IsDenied(err) {
		t.Fatalf("a moderator's Call was denied: %v", err)
	}
	// And an anonymous Ctx is a 401 rather than the panic ctx.User()
	// used to be.
	_, err := Retract.Call(app.Ctx(), RetractReq{ID: "anything"})
	if err == nil {
		t.Fatal("an anonymous Call was admitted")
	}
	if tako.IsDenied(err) {
		t.Fatalf("a missing session was reported as a refusal: %v", err)
	}
}

// A moderator retracting somebody else's post: the act, once past the
// door. This is what the handler is allowed to be about.
func TestAModeratorRetractsSomebodyElsesPost(t *testing.T) {
	srv := servePages(t)
	bob := client(t, srv)
	bobToken := signIn(t, bob, srv.URL, "bob")
	id := writePosts(t, bob, srv.URL, bobToken, "Bob post")[0]

	alice := freshClient(t)
	aliceToken := signInAs(t, alice, srv.URL, "alice", Moderator)
	res, err := alice.PostForm(srv.URL+"/moderate/"+id+"/retract", url.Values{"_csrf": {aliceToken}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("retract = %d, want 303", res.StatusCode)
	}

	// The reader's view is the proof: it is off the home list.
	res2, err := freshClient(t).Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	body, _ := io.ReadAll(res2.Body)
	if strings.Contains(string(body), "Bob post") {
		t.Errorf("the retracted post is still on the home page: %s", body)
	}
}
