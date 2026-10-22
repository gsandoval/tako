package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/routes"
	"github.com/gsandoval/tako/pkg/backends/local"
)

func TestDevSignInPreservesRolesOnUserSwitchAndClearsOnExplicitForm(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	RegisterRoles("admin", "editor")
	t.Cleanup(func() { RegisterRoles() })

	h := SessionHandler("google", "email")

	// 1. Sign in as alice from /_tako/signin with admin role checked.
	w1 := postSignIn(h, url.Values{
		"dev_user":       {"alice"},
		"dev_roles_form": {"1"},
		"dev_role":       {"admin"},
	})
	if w1.Code != http.StatusSeeOther {
		t.Fatalf("initial sign-in status = %d, want 303", w1.Code)
	}

	// 2. Switch to bob from the dev bar (no dev_roles_form).
	w2 := postSignIn(h, url.Values{"dev_user": {"bob"}})
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("switch to bob status = %d, want 303", w2.Code)
	}

	// 3. Switch back to alice from the dev bar (no dev_roles_form, no dev_role).
	w3 := postSignIn(h, url.Values{"dev_user": {"alice"}})
	if w3.Code != http.StatusSeeOther {
		t.Fatalf("switch back to alice status = %d, want 303", w3.Code)
	}
	cookie := sessionCookieFrom(t, w3)
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rec := loadSession(r)
	if rec == nil {
		t.Fatal("alice session did not load")
	}
	if len(rec.Roles) != 1 || rec.Roles[0] != "admin" {
		t.Fatalf("switching back to alice lost her roles: got %v, want [admin]", rec.Roles)
	}
	if rec.Provider != "google" {
		t.Errorf("provider = %q, want google (first declared OAuth provider)", rec.Provider)
	}
	if !rec.EmailVerified {
		t.Error("dev session EmailVerified = false, want true")
	}
	if !rec.user().EmailVerified {
		t.Error("sessionRecord.user().EmailVerified = false, want true")
	}

	// 4. Signing in from /_tako/signin (dev_roles_form=1) with NO roles checked
	// explicitly clears alice's roles.
	w4 := postSignIn(h, url.Values{
		"dev_user":       {"alice"},
		"dev_roles_form": {"1"},
	})
	cookie4 := sessionCookieFrom(t, w4)
	r4 := httptest.NewRequest("GET", "/", nil)
	r4.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie4})
	rec4 := loadSession(r4)
	if rec4 == nil {
		t.Fatal("alice session after clear did not load")
	}
	if len(rec4.Roles) != 0 {
		t.Fatalf("explicit empty role form did not clear roles: got %v, want []", rec4.Roles)
	}
}

func TestDevBarRendersRoleTogglesAndTogglesRoles(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	devBarTemplates(t)
	RegisterRoles("moderator", "admin")
	t.Cleanup(func() { RegisterRoles() })

	sh := SessionHandler("google")
	w := postSignIn(sh, url.Values{"dev_user": {"alice"}})
	cookie := sessionCookieFrom(t, w)

	page := tako.Page("GET /p", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return devBarSite.Render(ctx, "p.html", nil)
	})
	barHandler := WithDevBar(PageHandler(page, nil))

	req := httptest.NewRequest("GET", "/p", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rec := httptest.NewRecorder()
	barHandler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `name="dev_toggle_role" value="moderator"`) {
		t.Fatalf("dev bar missing moderator role toggle button:\n%s", body)
	}

	// Toggle moderator on via dev_toggle_role.
	wToggle := postSignIn(sh, url.Values{
		"dev_user":        {"alice"},
		"dev_toggle_role": {"moderator"},
	})
	cookie2 := sessionCookieFrom(t, wToggle)
	r2 := httptest.NewRequest("GET", "/p", nil)
	r2.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie2})
	srec := loadSession(r2)
	if srec == nil || len(srec.Roles) != 1 || srec.Roles[0] != "moderator" {
		t.Fatalf("after toggle on, roles = %v, want [moderator]", srec)
	}

	// Toggle moderator off via dev_toggle_role.
	wToggleOff := postSignIn(sh, url.Values{
		"dev_user":        {"alice"},
		"dev_toggle_role": {"moderator"},
	})
	cookie3 := sessionCookieFrom(t, wToggleOff)
	r3 := httptest.NewRequest("GET", "/p", nil)
	r3.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie3})
	srecOff := loadSession(r3)
	if srecOff == nil || len(srecOff.Roles) != 0 {
		t.Fatalf("after toggle off, roles = %v, want []", srecOff)
	}
}

func TestPasswordSignInRedirectsWhenNextIsProvided(t *testing.T) {
	h := signInHandler(t, "email")
	users := tako.Auth(tako.AuthConfig{Password: true}).Users()
	ctx := takoCtx(context.Background())

	if _, err := users.Create(ctx, tako.NewUser{
		Email: "ana@example.com", Name: "Ana", Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	w := postSignIn(h, url.Values{
		"email":    {"ana@example.com"},
		"password": {"correct horse battery"},
		"next":     {"/dashboard"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("password sign-in with next = %d, want 303 See Other", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}
}

func TestDevUsersEndpointRequiresNonceAndSupportsLifecycle(t *testing.T) {
	dir := t.TempDir()
	if err := BootLocal(dir); err != nil {
		t.Fatal(err)
	}
	defer swapNonce(t, "secret-dev-nonce")()

	var registered atomic.Value
	sub := tako.Subscribe(tako.Auth(tako.AuthConfig{Password: true}).Registered, func(ctx tako.Ctx, u tako.User) error {
		registered.Store(u)
		return nil
	})
	RegisterSubscriber("OnRegistered", sub)

	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := devReadyFirst(app)

	// 1. Missing nonce -> 403.
	reqNoNonce := httptest.NewRequest("POST", devUsersPath, strings.NewReader(`{"action":"list"}`))
	wNoNonce := httptest.NewRecorder()
	h.ServeHTTP(wNoNonce, reqNoNonce)
	if wNoNonce.Code != http.StatusForbidden {
		t.Fatalf("without nonce = %d, want 403", wNoNonce.Code)
	}

	callDevUsers := func(payload devUsersReq) (int, devUsersRes) {
		raw, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", devUsersPath, bytes.NewReader(raw))
		req.Header.Set("X-Tako-Dev-Nonce", "secret-dev-nonce")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var res devUsersRes
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		return w.Code, res
	}

	// 2. Create user -> fires auth.Registered() subscriber.
	code, created := callDevUsers(devUsersReq{
		Action:   "create",
		Email:    "cli@example.com",
		Password: "secret-password",
		Name:     "CLI User",
		Roles:    []string{"editor"},
	})
	if code != http.StatusOK || created.User == nil {
		t.Fatalf("create = %d %+v", code, created)
	}
	if err := WaitIdle(2 * time.Second); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
	gotReg, ok := registered.Load().(tako.User)
	if !ok || gotReg.Email != "cli@example.com" {
		t.Fatalf("auth.Registered subscriber got %+v, want cli@example.com", gotReg)
	}

	// 3. Mint a session for the user, then set_roles -> marks session stale.
	srec, err := mintSession(context.Background(), tako.User{
		ID: created.User.ID, Email: created.User.Email, Roles: created.User.Roles,
	})
	if err != nil {
		t.Fatal(err)
	}
	code, _ = callDevUsers(devUsersReq{
		Action: "set_roles",
		ID:     created.User.ID,
		Roles:  []string{"editor", "admin"},
	})
	if code != http.StatusOK {
		t.Fatalf("set_roles = %d", code)
	}
	rCheck := httptest.NewRequest("GET", "/", nil)
	rCheck.AddCookie(&http.Cookie{Name: sessionCookie, Value: srec.ID})
	refreshed := loadSession(rCheck)
	if refreshed == nil || len(refreshed.Roles) != 2 {
		t.Fatalf("session roles after set_roles = %+v, want [editor admin]", refreshed)
	}

	// 4. Disable user -> ends session.
	code, _ = callDevUsers(devUsersReq{
		Action:   "set_disabled",
		ID:       created.User.ID,
		Disabled: true,
	})
	if code != http.StatusOK {
		t.Fatalf("set_disabled = %d", code)
	}
	if loadSession(rCheck) != nil {
		t.Fatal("disabled user still has a live session")
	}
}

func TestLocalCronTriggerAndDeadline(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cronInert = true
	t.Cleanup(func() { cronInert = false })

	var ran atomic.Bool
	var hadDeadline atomic.Bool
	c := tako.Cron("every 1h", func(ctx tako.Ctx) error {
		ran.Store(true)
		_, ok := ctx.Deadline()
		hadDeadline.Store(ok)
		return nil
	}, tako.CronConfig{
		Timezone: "America/New_York",
		Deadline: 2 * time.Second,
	})
	RegisterCron("LocalSync", c)

	mux := http.NewServeMux()
	mux.Handle(routes.PatternCron, CronDispatch())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", routes.CronRoute("LocalSync"), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("local cron trigger = %d: %s", w.Code, w.Body.String())
	}
	if !ran.Load() {
		t.Fatal("local cron handler did not run")
	}
	if !hadDeadline.Load() {
		t.Fatal("cron handler context had no deadline despite CronConfig.Deadline")
	}
}

func TestLocalJobHonorsAttemptsAndTimeout(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if d, ok := hooks.Queue.(*local.QueueDispatcher); ok {
		d.SetQueueDelivery("_job:retry-job", 2, time.Millisecond, time.Millisecond)
	}
	job := tako.Job[struct{}]("retry-job")
	var calls atomic.Int32
	var sawDeadline atomic.Bool
	runner := tako.Runner(job, func(ctx tako.Ctx, _ struct{}) error {
		if _, ok := ctx.Deadline(); ok {
			sawDeadline.Store(true)
		}
		calls.Add(1)
		return errors.New("boom")
	}, tako.RunnerConfig{
		Attempts: 2,
		Timeout:  time.Second,
	})
	RegisterJob("RetryJob", runner)

	if err := job.Run(takoCtx(context.Background()), struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := WaitIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("job attempts = %d, want 2", got)
	}
	if !sawDeadline.Load() {
		t.Fatal("job handler context had no deadline despite RunnerConfig.Timeout")
	}
}

func sessionCookieFrom(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatal("no session cookie in response")
	return ""
}
