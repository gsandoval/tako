package gcp

// Accounts, on Identity Platform.
//
// Everything here is the identitytoolkit REST API, called with the
// service's own credentials — no Firebase Admin SDK, for the same reason
// tako verifies ID tokens with a raw JWKS fetch: it is a handful of JSON
// calls, and the SDK is a dependency (and a Node one, in the shape most
// people mean).
//
// A role is a CUSTOM CLAIM here, which is the whole reason two of these
// methods are read-modify-write. setCustomUserClaims replaces the entire
// claims blob, so tako reads what is there, changes the "roles" key, and
// writes it back — leaving alone whatever else the app or another tool
// put in it. The lock that makes that safe lives one layer up, in the
// SDK, because it is the same hazard on both backends.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gsandoval/tako/pkg/backends"
)

// UserStore implements the accounts contract against Identity Platform.
type UserStore struct{ c *Client }

// NewUserStore builds the accounts store on a shared client.
func NewUserStore(c *Client) *UserStore { return &UserStore{c: c} }

// rolesClaim is the custom claim tako owns. Everything else in the
// claims blob belongs to whoever put it there.
const rolesClaim = "roles"

// identityUser is Identity Platform's account shape, in the fields tako
// reads. customAttributes is a JSON STRING, not an object — the API
// nests one document inside another.
type identityUser struct {
	LocalID          string `json:"localId"`
	Email            string `json:"email"`
	DisplayName      string `json:"displayName"`
	CustomAttributes string `json:"customAttributes"`
	Disabled         bool   `json:"disabled"`
	EmailVerified    bool   `json:"emailVerified"`
}

func (u identityUser) record() (backends.UserRecord, error) {
	rec := backends.UserRecord{
		ID: u.LocalID, Email: u.Email, Name: u.DisplayName,
		Disabled: u.Disabled, EmailVerified: u.EmailVerified,
	}
	claims, err := u.claims()
	if err != nil {
		return backends.UserRecord{}, err
	}
	if raw, ok := claims[rolesClaim]; ok {
		var roles []string
		if err := json.Unmarshal(raw, &roles); err != nil {
			return backends.UserRecord{}, fmt.Errorf("account %s has a %q claim that is not a list of names: %v", u.LocalID, rolesClaim, err)
		}
		rec.Roles = roles
	}
	return rec, nil
}

// claims decodes the nested claims document, preserving every key as raw
// JSON so a write-back cannot lose one tako does not understand.
func (u identityUser) claims() (map[string]json.RawMessage, error) {
	if u.CustomAttributes == "" {
		return map[string]json.RawMessage{}, nil
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(u.CustomAttributes), &out); err != nil {
		return nil, fmt.Errorf("account %s has custom claims that are not a JSON object: %v", u.LocalID, err)
	}
	return out, nil
}

func (s *UserStore) Create(ctx context.Context, spec backends.NewUserSpec) (backends.UserRecord, error) {
	if err := backends.CheckRolesSize(spec.Roles); err != nil {
		return backends.UserRecord{}, err
	}
	// An address is one account, and the API's own duplicate error is a
	// generic 400. Asking first turns it into the error whose remedy is
	// obvious.
	if _, err := s.ByEmail(ctx, spec.Email); err == nil {
		return backends.UserRecord{}, backends.ErrEmailTaken
	} else if !errors.Is(err, backends.ErrNotFound) {
		return backends.UserRecord{}, err
	}

	in := map[string]any{
		"targetProjectId": s.c.Opts.Project,
		"email":           spec.Email,
	}
	if spec.Password != "" {
		in["password"] = spec.Password
	}
	if spec.Name != "" {
		in["displayName"] = spec.Name
	}
	var out struct {
		LocalID string `json:"localId"`
	}
	// signUp is the create: with a service-account credential it accepts
	// targetProjectId and the admin-only fields. The API-key form of the
	// same endpoint is the browser's, and is not what this is.
	if err := s.c.do(ctx, http.MethodPost, s.c.url(identityHost, "/v1/accounts:signUp"), in, &out); err != nil {
		return backends.UserRecord{}, s.explain(err, "create an account")
	}
	if len(spec.Roles) > 0 {
		if err := s.SetRoles(ctx, out.LocalID, spec.Roles); err != nil {
			return backends.UserRecord{}, err
		}
	}
	return s.Get(ctx, out.LocalID)
}

func (s *UserStore) Get(ctx context.Context, id string) (backends.UserRecord, error) {
	u, err := s.lookup(ctx, map[string]any{"localId": []string{id}})
	if err != nil {
		return backends.UserRecord{}, err
	}
	return u.record()
}

func (s *UserStore) ByEmail(ctx context.Context, email string) (backends.UserRecord, error) {
	u, err := s.lookup(ctx, map[string]any{"email": []string{email}})
	if err != nil {
		return backends.UserRecord{}, err
	}
	return u.record()
}

func (s *UserStore) lookup(ctx context.Context, in map[string]any) (identityUser, error) {
	var out struct {
		Users []identityUser `json:"users"`
	}
	url := s.c.url(identityHost, "/v1/projects/%s/accounts:lookup", s.c.Opts.Project)
	if err := s.c.do(ctx, http.MethodPost, url, in, &out); err != nil {
		return identityUser{}, s.explain(err, "read an account")
	}
	if len(out.Users) == 0 {
		// A lookup that matches nothing answers 200 with no users, not
		// a 404 — so absence has to be recognised here or it would read
		// as success with an empty account.
		return identityUser{}, backends.ErrNotFound
	}
	return out.Users[0], nil
}

func (s *UserStore) List(ctx context.Context, limit int, after string) ([]backends.UserRecord, string, error) {
	if limit <= 0 {
		limit = backends.UsersLimitDefault
	}
	if limit > backends.UsersLimitMax {
		limit = backends.UsersLimitMax
	}
	q := url.Values{"maxResults": {strconv.Itoa(limit)}}
	if after != "" {
		q.Set("nextPageToken", after)
	}
	var out struct {
		Users         []identityUser `json:"users"`
		NextPageToken string         `json:"nextPageToken"`
	}
	u := s.c.url(identityHost, "/v1/projects/%s/accounts:batchGet", s.c.Opts.Project) + "?" + q.Encode()
	if err := s.c.do(ctx, http.MethodGet, u, nil, &out); err != nil {
		return nil, "", s.explain(err, "list accounts")
	}
	recs := make([]backends.UserRecord, 0, len(out.Users))
	for _, u := range out.Users {
		rec, err := u.record()
		if err != nil {
			return nil, "", err
		}
		recs = append(recs, rec)
	}
	return recs, out.NextPageToken, nil
}

// SetRoles rewrites the roles claim and leaves every other claim alone.
func (s *UserStore) SetRoles(ctx context.Context, id string, roles []string) error {
	if err := backends.CheckRolesSize(roles); err != nil {
		return err
	}
	current, err := s.lookup(ctx, map[string]any{"localId": []string{id}})
	if err != nil {
		return err
	}
	claims, err := current.claims()
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		// Clearing means removing the key, not writing an empty list: a
		// claims blob should not accumulate empty keys for every role
		// anybody ever revoked.
		delete(claims, rolesClaim)
	} else {
		raw, err := json.Marshal(roles)
		if err != nil {
			return err
		}
		claims[rolesClaim] = raw
	}
	encoded, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	return s.update(ctx, map[string]any{
		"localId":          id,
		"customAttributes": string(encoded),
	})
}

func (s *UserStore) SetDisabled(ctx context.Context, id string, disabled bool) error {
	return s.update(ctx, map[string]any{"localId": id, "disableUser": disabled})
}

func (s *UserStore) update(ctx context.Context, in map[string]any) error {
	in["targetProjectId"] = s.c.Opts.Project
	u := s.c.url(identityHost, "/v1/projects/%s/accounts:update", s.c.Opts.Project)
	if err := s.c.do(ctx, http.MethodPost, u, in, nil); err != nil {
		return s.explain(err, "update an account")
	}
	return nil
}

func (s *UserStore) Delete(ctx context.Context, id string) error {
	u := s.c.url(identityHost, "/v1/projects/%s/accounts:delete", s.c.Opts.Project)
	err := s.c.do(ctx, http.MethodPost, u, map[string]any{"localId": id}, nil)
	if err == nil {
		return nil
	}
	// Deleting what is not there is not a failure — the contract says so,
	// and the API disagrees.
	var se *statusError
	if errors.As(err, &se) && (se.Code == http.StatusNotFound || se.Code == http.StatusBadRequest) {
		return nil
	}
	return s.explain(err, "delete an account")
}

// explain turns the two failures an operator can actually act on into
// sentences that say what to do. Everything else passes through.
//
// They are different problems: a 403 means this service was never
// granted the role (its package may have started calling auth.Users()
// since the last deploy), and a 403 naming the API means Identity
// Platform is not turned on in the project at all.
func (s *UserStore) explain(err error, what string) error {
	var se *statusError
	if !errors.As(err, &se) {
		return err
	}
	switch {
	case se.Code == http.StatusForbidden && containsAny(se.Body, "SERVICE_DISABLED", "has not been used in project", "is disabled"):
		return fmt.Errorf("tako: the Identity Platform API is not enabled on %s — enable identitytoolkit.googleapis.com (tako deploy does this; a service running against a project it did not provision does not get it)", s.c.Opts.Project)
	// The two billing-attribution refusals, ahead of the generic 403
	// because they wear the same status and have nothing to do with a
	// role. Granting identitytoolkit.admin for either changes nothing,
	// which is what the old message sent an operator off to do (FL22).
	case se.Code == http.StatusForbidden && strings.Contains(se.Body, "serviceusage.services.use"):
		return fmt.Errorf("tako: cannot bill identitytoolkit calls to %s — grant:\n"+
			"        gcloud projects add-iam-policy-binding %s \\\n"+
			"          --member user:$(gcloud config get account) \\\n"+
			"          --role roles/serviceusage.serviceUsageConsumer",
			s.c.Opts.Project, s.c.Opts.Project)
	case containsAny(se.Body, "requires a quota project", "quota project"):
		return fmt.Errorf("tako: identitytoolkit.googleapis.com needs a billing project on %s and this "+
			"credential carries none — tako sends one for user credentials from "+
			"`gcloud auth application-default login`. On a service-account key file, use ADC "+
			"instead, or set GOOGLE_CLOUD_QUOTA_PROJECT.", s.c.Opts.Project)
	case se.Code == http.StatusForbidden || se.Code == http.StatusUnauthorized:
		return fmt.Errorf("tako: this service may not %s on %s — auth.Users() derives roles/identitytoolkit.admin at deploy time, so re-run tako deploy if this service started using it since the last one (%s)", what, s.c.Opts.Project, se.Body)
	}
	return err
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
