package gcp

// Secret Manager, for `tako secrets` (SC4).
//
// tako has never created a secret: a value is the user's data, so the
// deploy verifies existence and preflight prints the command. What it
// could not do was help you RUN that command, which is all this is.
//
// One thing deliberately absent: these secrets carry no tako label.
// Everything the reconciler labels is something `tako destroy` and
// `tako prune` may sweep, and a swept secret is user data destroyed by
// a command that never named it (settled decision 12). A secret is
// removed by asking for it and nothing else.

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// SecretState is what an operator needs to decide anything about a
// secret WITHOUT seeing its value.
type SecretState struct {
	// Physical is the name in Secret Manager: env-prefixed, so one
	// declaration is a different secret per environment. In prod it is
	// the declared name unchanged (Prefixed leaves the default env
	// alone), which is worth showing rather than assuming.
	Physical string
	Exists   bool
	Versions int
	Created  time.Time
	Updated  time.Time
}

// SecretState reads existence and version count. It never reads a
// value: nothing in this file calls :access, so no code path here can
// put a secret on a terminal.
func (c *Client) SecretState(ctx context.Context, physical string) (SecretState, error) {
	st := SecretState{Physical: physical}
	var meta struct {
		CreateTime time.Time `json:"createTime"`
	}
	u := c.url(secretHost, "/v1/projects/%s/secrets/%s", c.Project, url.PathEscape(physical))
	if err := c.do(ctx, http.MethodGet, u, nil, &meta); err != nil {
		if ae, ok := asAPIError(err); ok && ae.Status == http.StatusNotFound {
			return st, nil
		}
		return st, Classify(err, c.Project, "read secret metadata", "roles/secretmanager.viewer")
	}
	st.Exists = true
	st.Created = meta.CreateTime

	var versions struct {
		Versions []struct {
			State      string    `json:"state"`
			CreateTime time.Time `json:"createTime"`
		} `json:"versions"`
	}
	vu := c.url(secretHost, "/v1/projects/%s/secrets/%s/versions", c.Project, url.PathEscape(physical))
	if err := c.do(ctx, http.MethodGet, vu, nil, &versions); err != nil {
		return st, Classify(err, c.Project, "list secret versions", "roles/secretmanager.viewer")
	}
	for _, v := range versions.Versions {
		if v.State == "DESTROYED" {
			continue
		}
		st.Versions++
		if v.CreateTime.After(st.Updated) {
			st.Updated = v.CreateTime
		}
	}
	return st, nil
}

// SetSecret creates the secret if it is absent and adds a version.
//
// Adding rather than replacing: a version is the unit Secret Manager
// audits, and services read :latest, so a new version IS the update.
// Nothing here disables the previous one — that is rotation, and it has
// an ordering tako has no opinion about.
func (c *Client) SetSecret(ctx context.Context, physical, value string) (created bool, err error) {
	st, err := c.SecretState(ctx, physical)
	if err != nil {
		return false, err
	}
	if !st.Exists {
		u := c.url(secretHost, "/v1/projects/%s/secrets?secretId=%s", c.Project, url.QueryEscape(physical))
		body := map[string]any{"replication": map[string]any{"automatic": map[string]any{}}}
		if err := c.do(ctx, http.MethodPost, u, body, nil); err != nil {
			return false, Classify(err, c.Project, "create the secret", "roles/secretmanager.admin")
		}
		created = true
	}
	u := c.url(secretHost, "/v1/projects/%s/secrets/%s:addVersion", c.Project, url.PathEscape(physical))
	body := map[string]any{"payload": map[string]any{
		"data": base64.StdEncoding.EncodeToString([]byte(value)),
	}}
	if err := c.do(ctx, http.MethodPost, u, body, nil); err != nil {
		return created, Classify(err, c.Project, "add a secret version", "roles/secretmanager.admin")
	}
	return created, nil
}

// DeleteSecret destroys the secret and every version it has.
//
// The bottom rung of the confirmation ladder, and the CLI enforces the
// typed confirmation — this function only does what it is told, which
// is why it is named for exactly what it does. Secret Manager has no
// undelete: the versions are the audit trail and they go too.
func (c *Client) DeleteSecret(ctx context.Context, physical string) error {
	u := c.url(secretHost, "/v1/projects/%s/secrets/%s", c.Project, url.PathEscape(physical))
	if err := c.do(ctx, http.MethodDelete, u, nil, nil); err != nil {
		if ae, ok := asAPIError(err); ok && ae.Status == http.StatusNotFound {
			return fmt.Errorf("%s does not exist in %s — nothing to delete", physical, c.Project)
		}
		return Classify(err, c.Project, "delete the secret", "roles/secretmanager.admin")
	}
	return nil
}

// There is deliberately no SecretName here. The physical name comes
// from pkg/backends/gcp's, which is what a deployed service resolves
// with — a second copy would be one more pair that must agree, and the
// half that drifts addresses a secret nobody reads.
