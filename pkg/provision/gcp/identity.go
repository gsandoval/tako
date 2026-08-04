package gcp

import (
	"context"
	"fmt"
	"net/http"
)

// Service agents: the identities Google runs on your behalf.
//
// A Pub/Sub alert channel is delivered by Cloud Monitoring's own agent,
// not by anything tako runs, so that agent needs publisher on the topic
// the channel names. The agent does not exist in a fresh project until
// something asks for it.
//
// tako used to write the grant anyway, on the assumption that granting
// to an agent that does not exist yet is accepted. It is not, and the
// live walk is where that was found — Pub/Sub answers:
//
//	Service account service-<number>@gcp-sa-monitoring-notification.iam.gserviceaccount.com
//	does not exist. (HTTP 400)
//
// which failed the whole deploy after 56 objects had been created. The
// assumption had never been tested because no example declared a
// Pub/Sub channel until siren.

// EnsureServiceIdentity provisions the service agent for one Google API
// and returns its email.
//
// This is what `gcloud beta services identity create` calls. It is
// idempotent — an existing agent comes back with the same address — so
// a deploy can call it every time rather than checking first.
func (c *Client) EnsureServiceIdentity(ctx context.Context, service string) (string, error) {
	url := c.url(serviceuseHost, "/v1beta1/projects/%s/services/%s:generateServiceIdentity", c.Project, service)
	var out struct {
		Response struct {
			Email string `json:"email"`
		} `json:"response"`
	}
	if err := c.do(ctx, http.MethodPost, url, map[string]any{}, &out); err != nil {
		return "", Classify(err, c.Project, fmt.Sprintf("provision the %s service agent", service), "roles/serviceusage.serviceUsageAdmin")
	}
	return out.Response.Email, nil
}

// MonitoringNotificationAgent is the API whose agent delivers Pub/Sub
// alert channels. Named here so the deploy and the grant derivation
// cannot disagree about which service to provision.
const MonitoringNotificationAgent = "monitoring.googleapis.com"
