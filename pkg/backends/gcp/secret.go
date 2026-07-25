package gcp

import (
	"context"
	"fmt"
	"net/url"
)

// SecretName is the physical Secret Manager secret for a declared
// tako.Secret: env-prefixed like every other logical name. tako never
// creates, writes, or deletes secrets — values are data the user owns;
// preflight checks existence and prints the exact create command.
func SecretName(env, base string) string {
	return Prefixed(env, base)
}

// AccessSecret reads the latest version of one declared secret. Boot
// calls it once per declared secret before serving — the same contract
// as local's .env: a missing secret refuses boot, so SecretHandle.Value
// never fails at request time.
func (c *Client) AccessSecret(ctx context.Context, base string) (string, error) {
	u := "https://secretmanager.googleapis.com/v1/projects/" + c.Opts.Project +
		"/secrets/" + url.PathEscape(SecretName(c.Opts.Env, base)) + "/versions/latest:access"
	var out struct {
		Payload struct {
			Data []byte `json:"data"` // encoding/json base64-decodes
		} `json:"payload"`
	}
	if err := c.do(ctx, "GET", u, nil, &out); err != nil {
		return "", fmt.Errorf("secret %q: %w", base, err)
	}
	return string(out.Payload.Data), nil
}
