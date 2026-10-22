package runtime

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/routes"
	"github.com/gsandoval/tako/internal/schedule"
)

// cronInert, set by BootGCP, keeps crons from ticking in-process in
// the cloud (per-instance tickers under autoscaling are not a
// schedule); Cloud Scheduler drives them through /_tako/api/cron instead.
var cronInert bool

// cronHandlers maps cron name → invoke, for the scheduler dispatch
// route. Populated by RegisterCron on every backend.
var cronHandlers sync.Map

// RegisterCron starts the named cron on its schedule. Generated mains
// call it once per cron at startup; runs are serialized per cron — a
// tick landing mid-run is skipped with a narration. WithSingleflight
// additionally wraps the handler itself in the §4.4 lease (done at
// declaration, so every trigger inherits it): overlapping runs
// collapse across instances, narrated with the ↻ line.
func RegisterCron(name string, h tako.Handler) {
	kind, schedStr, invoke := hooks.HandlerInfo(h)
	if kind != "cron" {
		panic(fmt.Sprintf("tako: RegisterCron(%s): handler is %q, not a cron", name, kind))
	}
	fn, ok := invoke.(func(tako.Ctx, []byte) error)
	if !ok {
		panic("tako: RegisterCron before Boot")
	}
	sched, err := schedule.Parse(schedStr)
	if err != nil {
		// The analyzer rejects unparseable schedules; reaching this means
		// the binary predates the declaration — fail loudly.
		panic("tako: " + err.Error())
	}
	var tzName string
	var deadline time.Duration
	if hooks.HandlerCronConfig != nil {
		tzName, deadline = hooks.HandlerCronConfig(h)
	}
	loc := time.Local
	if tzName != "" {
		if loaded, err := time.LoadLocation(tzName); err == nil {
			loc = loaded
		}
	}
	runCron := fn
	if deadline > 0 {
		base := fn
		runCron = func(ctx tako.Ctx, b []byte) error {
			c, cancel := context.WithTimeout(ctx, deadline)
			defer cancel()
			return base(&requestCtx{Context: c, log: ctx.Log()}, b)
		}
	}
	// The declared name reaches the SDK here: singleflight keys its
	// lease on it, and it is the only identity stable across instances
	// and deploys.
	hooks.SetHandlerIdentity(hooks.HandlerID(h), name)
	cronHandlers.Store(name, runCron)
	if cronInert {
		// In the cloud the trigger is Cloud Scheduler → POST
		// /_tako/api/cron/<name> (T2); a per-instance ticker would fire
		// once per live instance and never when scaled to zero.
		slog.Default().Info("cron driven by Cloud Scheduler", "cron", name, "schedule", schedStr)
		return
	}
	fmt.Printf("⏰ cron %s: %s\n", name, schedStr)
	go func() {
		var running atomic.Bool
		for next := sched.Next(time.Now().In(loc)); ; next = sched.Next(next.In(loc)) {
			time.Sleep(time.Until(next))
			if !running.CompareAndSwap(false, true) {
				fmt.Printf("⏰ cron %s: run still in progress — tick skipped\n", name)
				continue
			}
			go func() {
				defer running.Store(false)
				start := time.Now()
				cctx := &requestCtx{Context: context.Background(), log: slog.Default().With("cron", name)}
				if err := runCron(cctx, nil); err != nil {
					fmt.Printf("⏰ cron %s failed: %v — next run on schedule\n", name, err)
					return
				}
				fmt.Printf("⏰ cron %s ok (%s)\n", name, time.Since(start).Round(time.Millisecond))
			}()
		}
	}()
}

// CronDispatch serves POST /_tako/api/cron/{cron}: Cloud Scheduler's
// trigger. The route may sit on a public service, so the request
// authenticates itself: a Google-signed OIDC token whose audience is
// this service (TAKO_CRON_AUDIENCE) and whose identity is the
// service's own SA (TAKO_CRON_INVOKER) — both injected at deploy. The
// run is synchronous; Scheduler's attempt deadline is the timeout.
func CronDispatch() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("cron")
		// One mux pattern serves every cron in the service, so the
		// request's own pattern cannot identify which one ran. Name it,
		// or all crons share a metric series and CronMissed cannot tell
		// a dead cron from a healthy one beside it.
		RefineRoute(r, routes.CronRoute(name))
		if status, err := verifyCronCaller(r, name); err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		v, found := cronHandlers.Load(name)
		if !found {
			// Narrated for the same reason the auth refusals are: a 404 from
			// tako and a 404 from Cloud Run's own router are the same three
			// digits, and this one means the schedule outlived the handler.
			err := fmt.Errorf("no cron named %q in this service", name)
			logRefusal(slog.Default().With("cron", name), "cron trigger", http.StatusNotFound, err)
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		start := time.Now()
		cctx := &requestCtx{Context: r.Context(), log: slog.Default().With("cron", name)}
		if err := v.(func(tako.Ctx, []byte) error)(cctx, nil); err != nil {
			// A 500 makes Scheduler retry per its policy; the error is
			// the run's, already logged with the cron's label.
			slog.Default().Error("cron run failed", "cron", name, "err", err)
			http.Error(w, "cron run failed", http.StatusInternalServerError)
			return
		}
		slog.Default().Info("cron ok", "cron", name, "took", time.Since(start).Round(time.Millisecond).String())
		w.WriteHeader(http.StatusOK)
	})
}

// --- Google-signed OIDC verification (Scheduler's identity) -----------
//
// Same pure-stdlib RS256 discipline as the session's Identity Platform
// path, against Google's OAuth2 x509 endpoint.

const googleCerts = "https://www.googleapis.com/oauth2/v1/certs"

var (
	gcertMu      sync.Mutex
	gcertCache   map[string]*rsa.PublicKey
	gcertExpires time.Time
)

func googleSigningKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	gcertMu.Lock()
	defer gcertMu.Unlock()
	if gcertCache != nil && time.Now().Before(gcertExpires) {
		return gcertCache, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleCerts, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var pems map[string]string
	if err := json.NewDecoder(res.Body).Decode(&pems); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for kid, certPEM := range pems {
		block, _ := pem.Decode([]byte(certPEM))
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if pub, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			keys[kid] = pub
		}
	}
	gcertCache, gcertExpires = keys, time.Now().Add(30*time.Minute)
	return keys, nil
}

// verifyCronCaller authenticates a Cloud Scheduler trigger against this
// service's deploy-injected expectations, mirroring verifyPushCaller:
// same token shape, same expectation pair, same refusal narration. Two
// differences are deliberate and both are this route's existing
// behaviour, left alone inside a logging fix and noted as item 50: there
// is no local skip (locally the in-process ticker drives crons, so this
// route only ever hears strangers), and the checks run token-first, so a
// service that is BOTH unconfigured and unaddressed answers 401 rather
// than push's 500.
func verifyCronCaller(r *http.Request, name string) (int, error) {
	audience, invoker := os.Getenv("TAKO_CRON_AUDIENCE"), os.Getenv("TAKO_CRON_INVOKER")
	_, registered := cronHandlers.Load(name)
	if isLocalBackend() && audience == "" && invoker == "" && r.Header.Get("Authorization") == "" &&
		(registered || os.Getenv("TAKO_BACKEND") == "local" || activeDevNonce() != "") {
		return 0, nil
	}
	refuse := func(status int, err error) (int, error) {
		logRefusal(slog.Default().With("cron", name), "cron trigger", status, err,
			"expected_audience", audience, "expected_invoker", invoker)
		return status, err
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return refuse(http.StatusUnauthorized, errors.New("cron triggers carry an OIDC bearer token"))
	}
	if audience == "" || invoker == "" {
		return refuse(http.StatusForbidden, errors.New(
			"cron trigger rejected: this service has no cron trigger identity configured"))
	}
	if err := verifyGoogleToken(r.Context(), token, audience, invoker); err != nil {
		return refuse(http.StatusForbidden, fmt.Errorf("cron trigger rejected: %v", err))
	}
	return 0, nil
}

// logRefusal narrates a refused trigger: WARNING severity, the reason,
// and whatever the route can offer toward fixing it (item 49).
//
// Without it the only trace is the platform's own request line, and
// tako's 403 is indistinguishable from Cloud Run's from outside the
// container — a stale audience after a service URL change and a missing
// run.invoker grant look identical while having different fixes. The
// expectations travel in the log rather than only in the response body
// because the caller is Pub/Sub or Scheduler, which reads nothing; and
// comparing them against the deliverer's own OIDC config IS the
// remediation. Neither is a secret: a service URL and an SA email, both
// readable from the deploy.
//
// One implementation for both routes, for the reason verifyGoogleToken
// is one implementation: a second copy is how one route ends up quieter
// than the other.
func logRefusal(log *slog.Logger, what string, status int, reason error, extra ...any) {
	// reason.Error(), not the error: a JSON handler marshals a bare
	// errors.errorString as {} and the reason vanishes.
	log.Warn("tako refused this "+what,
		append([]any{"status", status, "reason", reason.Error()}, extra...)...)
}

// verifyGoogleToken is the check itself, with the expectations passed
// in: Cloud Scheduler drives crons and Pub/Sub push (and Cloud Tasks)
// drives workers, and all three present the same kind of token against
// the same kind of pair. One implementation, because a second copy is
// how one of the two routes ends up with a weaker check than the other
// — which is the state CR3 found.
func verifyGoogleToken(ctx context.Context, token, audience, invoker string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("not a JWT")
	}
	headRaw, err1 := b64url(parts[0])
	claimRaw, err2 := b64url(parts[1])
	sig, err3 := b64url(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return errors.New("malformed JWT")
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(headRaw, &head) != nil || head.Alg != "RS256" {
		return errors.New("unexpected algorithm")
	}
	keys, err := googleSigningKeys(ctx)
	if err != nil {
		return fmt.Errorf("fetching Google signing keys: %v", err)
	}
	key, ok := keys[head.Kid]
	if !ok {
		return errors.New("unknown signing key")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return errors.New("bad signature")
	}
	var claims struct {
		Iss   string `json:"iss"`
		Aud   string `json:"aud"`
		Exp   int64  `json:"exp"`
		Email string `json:"email"`
	}
	if json.Unmarshal(claimRaw, &claims) != nil {
		return errors.New("malformed claims")
	}
	if claims.Iss != "https://accounts.google.com" && claims.Iss != "accounts.google.com" {
		return errors.New("not a Google-signed token")
	}
	if claims.Aud != audience {
		return errors.New("token audience is not this service")
	}
	if claims.Email != invoker {
		return fmt.Errorf("token identity %q is not this service's scheduler invoker", claims.Email)
	}
	if time.Now().Unix() > claims.Exp {
		return errors.New("token expired")
	}
	return nil
}
