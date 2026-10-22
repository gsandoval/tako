package runtime

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

const (
	devUsersPath      = "/_tako/dev/users"
	devReadyAliasPath = "/_tako/dev/ready"
)

var (
	initDevNonce = os.Getenv("TAKO_DEV_NONCE")
	localDataDir string
)

func activeDevNonce() string {
	if devNonce != initDevNonce {
		return devNonce
	}
	if env := os.Getenv("TAKO_DEV_NONCE"); env != initDevNonce {
		return env
	}
	return devNonce
}

type devInfoFile struct {
	URL   string `json:"url"`
	Nonce string `json:"nonce"`
}

func writeDevInfo(dir, port, nonce string) {
	if dir == "" || nonce == "" {
		return
	}
	if port == "" {
		port = "8080"
	}
	_ = os.MkdirAll(dir, 0o755)
	raw, err := json.Marshal(devInfoFile{
		URL:   "http://127.0.0.1:" + port,
		Nonce: nonce,
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "dev.json"), raw, 0o600)
}

func removeDevInfo(dir string) {
	if dir == "" {
		return
	}
	_ = os.Remove(filepath.Join(dir, "dev.json"))
}

type devUsersReq struct {
	Action   string   `json:"action"`
	ID       string   `json:"id,omitempty"`
	Email    string   `json:"email,omitempty"`
	Password string   `json:"password,omitempty"`
	Name     string   `json:"name,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	After    string   `json:"after,omitempty"`
}

type devUsersRes struct {
	User  *backends.UserRecord  `json:"user,omitempty"`
	Users []backends.UserRecord `json:"users,omitempty"`
	Next  string                `json:"next,omitempty"`
	Error string                `json:"error,omitempty"`
	Code  string                `json:"code,omitempty"`
}

func answerDevUsers(w http.ResponseWriter, r *http.Request) bool {
	nonce := activeDevNonce()
	if nonce == "" || !isLocalBackend() || r.URL.Path != devUsersPath {
		return false
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	got := r.Header.Get("X-Tako-Dev-Nonce")
	if subtle.ConstantTimeCompare([]byte(got), []byte(nonce)) != 1 {
		http.Error(w, "forbidden: missing or invalid X-Tako-Dev-Nonce", http.StatusForbidden)
		return true
	}
	if hooks.Users == nil {
		writeJSON(w, http.StatusServiceUnavailable, devUsersRes{Error: "users backend is not wired"})
		return true
	}
	var req devUsersReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, devUsersRes{Error: err.Error()})
		return true
	}
	ctx := r.Context()
	switch req.Action {
	case "create":
		rec, err := hooks.Users.Create(ctx, backends.NewUserSpec{
			ID:       req.ID,
			Email:    req.Email,
			Password: req.Password,
			Name:     req.Name,
			Roles:    req.Roles,
		})
		if err != nil {
			writeDevUsersErr(w, err)
			return true
		}
		publishRegisteredUser(ctx, rec)
		writeJSON(w, http.StatusOK, devUsersRes{User: &rec})
	case "get":
		rec, err := hooks.Users.Get(ctx, req.ID)
		if err != nil {
			writeDevUsersErr(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, devUsersRes{User: &rec})
	case "by_email":
		rec, err := hooks.Users.ByEmail(ctx, req.Email)
		if err != nil {
			writeDevUsersErr(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, devUsersRes{User: &rec})
	case "list":
		list, next, err := hooks.Users.List(ctx, req.Limit, req.After)
		if err != nil {
			writeDevUsersErr(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, devUsersRes{Users: list, Next: next})
	case "set_roles":
		if err := hooks.Users.SetRoles(ctx, req.ID, req.Roles); err != nil {
			writeDevUsersErr(w, err)
			return true
		}
		_ = StaleSessions(ctx, req.ID)
		writeJSON(w, http.StatusOK, devUsersRes{})
	case "set_disabled":
		if err := hooks.Users.SetDisabled(ctx, req.ID, req.Disabled); err != nil {
			writeDevUsersErr(w, err)
			return true
		}
		if req.Disabled {
			_ = EndSessions(ctx, req.ID)
		}
		writeJSON(w, http.StatusOK, devUsersRes{})
	case "delete":
		if err := hooks.Users.Delete(ctx, req.ID); err != nil {
			writeDevUsersErr(w, err)
			return true
		}
		_ = EndSessions(ctx, req.ID)
		writeJSON(w, http.StatusOK, devUsersRes{})
	default:
		writeJSON(w, http.StatusBadRequest, devUsersRes{Error: "unknown action: " + req.Action})
	}
	return true
}

func publishRegisteredUser(ctx context.Context, rec backends.UserRecord) {
	if hooks.Topic == nil {
		return
	}
	u := tako.User{
		ID:            rec.ID,
		Email:         rec.Email,
		Name:          rec.Name,
		Roles:         rec.Roles,
		EmailVerified: rec.EmailVerified,
	}
	raw, err := json.Marshal(u)
	if err != nil {
		return
	}
	_ = hooks.Topic.Publish(ctx, hooks.AuthRegisteredSource, raw)
}

func writeDevUsersErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backends.ErrNotFound):
		writeJSON(w, http.StatusNotFound, devUsersRes{Error: err.Error(), Code: "not_found"})
	case errors.Is(err, backends.ErrEmailTaken):
		writeJSON(w, http.StatusConflict, devUsersRes{Error: err.Error(), Code: "email_taken"})
	case errors.Is(err, backends.ErrRolesTooLarge):
		writeJSON(w, http.StatusBadRequest, devUsersRes{Error: err.Error(), Code: "roles_too_large"})
	default:
		writeJSON(w, http.StatusInternalServerError, devUsersRes{Error: err.Error()})
	}
}
