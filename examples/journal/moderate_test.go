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
	signIn(t, alice, srv.URL, "alice")
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
// The identity is signed in the local way and then judged as a deploy
// would judge it: journal's policy admits .test addresses only while
// EnvLocal, so flipping the environment is how a test asks "and in
// production?" without a second sign-in path.
func TestAStrangerAtTheDeskGetsAPageThatSaysWhy(t *testing.T) {
	srv := servePages(t)
	c := client(t, srv)
	signIn(t, c, srv.URL, "alice") // alice@example.test, not on the allowlist

	t.Setenv("TAKO_BACKEND", "gcp") // as a deploy sets it
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
	if !strings.Contains(string(body), "limited to this journal") {
		t.Errorf("the refusal lost the policy's own words: %s", body)
	}
	// It is still the app's page, chrome and all — the thing a status
	// page exists for.
	if !strings.Contains(string(body), "Back to the journal") {
		t.Errorf("the refusal is not the app's page: %s", body)
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

	stranger := tako.User{ID: "s", Email: "stranger@example.com", Provider: "google"}
	if _, err := Retract.Call(app.Ctx(stranger), RetractReq{ID: "anything"}); !tako.IsDenied(err) {
		t.Fatalf("a stranger's Call was not denied: %v", err)
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
	aliceToken := signIn(t, alice, srv.URL, "alice")
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
