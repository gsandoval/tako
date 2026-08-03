package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gsandoval/tako/pkg/provision"
)

// AlertChannels and AlertPolicies manage Cloud Monitoring notification
// channels and alert policies.
//
// Both carry `userLabels`, so ownership and the spec hash ride there and
// label-based reconciliation works unchanged — no state file, same as
// every other kind. Neither API lists by label filter, so observation
// lists everything and keeps what carries tako's labels; a project has
// tens of these, not thousands.
//
// The NAME does not ride a label, and cannot: GCP label values match
// [\p{Ll}\p{Lo}\p{N}_-] — no uppercase — while a tako identity is a Go
// VARIABLE name, and an exported one is capitalized by the language.
// `tako-name: "Stuck"` is refused with
// `character "S" at position 0 is not a non-uppercased letter`.
// So the name rides the displayName, which has no such charset, and
// observation reads it back by removing the env prefix — the same rule
// Config.Name applied to write it.

// AlertChannels owns notification channels.
type AlertChannels struct {
	C      *Client
	App    string
	Env    string
	Labels map[string]string
}

func (a *AlertChannels) Kind() provision.Kind { return provision.KindAlertChannel }

func (a *AlertChannels) base() string {
	return a.C.url(monitoringHost, "/v3/projects/%s/notificationChannels", a.C.Project)
}

type notificationChannel struct {
	Name        string            `json:"name,omitempty"`
	Type        string            `json:"type,omitempty"`
	DisplayName string            `json:"displayName,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	UserLabels  map[string]string `json:"userLabels,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
}

// channelLabels renders the destination into the label the API wants for
// this channel type. The key names are Google's, one per type.
func channelLabels(kind, target string) map[string]string {
	switch kind {
	case "email":
		return map[string]string{"email_address": target}
	case "google_chat":
		return map[string]string{"space": target}
	case "webhook_tokenauth":
		return map[string]string{"url": target}
	case "pubsub":
		return map[string]string{"topic": target}
	}
	return nil
}

func (a *AlertChannels) List(ctx context.Context) ([]provision.Observed, error) {
	var out struct {
		Channels []notificationChannel `json:"notificationChannels"`
	}
	if err := a.C.do(ctx, http.MethodGet, a.base(), nil, &out); err != nil {
		if disabledAPI(err) {
			return nil, nil
		}
		return nil, Classify(err, a.C.Project, "list notification channels", "roles/monitoring.notificationChannelEditor")
	}
	var observed []provision.Observed
	for _, ch := range out.Channels {
		if !ownedBy(ch.UserLabels, a.App, a.Env) {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind: provision.KindAlertChannel, Name: unprefixed(ch.DisplayName, a.Env),
			SpecHash: ch.UserLabels["tako-spec-hash"], ID: ch.Name,
		})
	}
	return observed, nil
}

func (a *AlertChannels) body(o provision.Object) notificationChannel {
	kind := fmt.Sprint(o.Spec["channelKind"])
	labels := map[string]string{"tako-spec-hash": provision.SpecHash(o.Spec)}
	for k, v := range a.Labels {
		labels[k] = v
	}
	enabled := true
	return notificationChannel{
		Type:        kind,
		DisplayName: fmt.Sprint(o.Spec["displayName"]),
		Labels:      channelLabels(kind, fmt.Sprint(o.Spec["target"])),
		UserLabels:  labels,
		Enabled:     &enabled,
	}
}

func (a *AlertChannels) Create(ctx context.Context, o provision.Object) error {
	if err := a.C.do(ctx, http.MethodPost, a.base(), a.body(o), nil); err != nil {
		return Classify(err, a.C.Project, "create the notification channel", "roles/monitoring.notificationChannelEditor")
	}
	return nil
}

// Update replaces by name: a channel's type cannot be patched, and the
// destination is most of the spec, so recreate is the honest shape.
func (a *AlertChannels) Update(ctx context.Context, o provision.Object) error {
	if err := a.Delete(ctx, o.Name); err != nil {
		return err
	}
	return a.Create(ctx, o)
}

func (a *AlertChannels) Delete(ctx context.Context, name string) error {
	id, err := a.idOf(ctx, name)
	if err != nil || id == "" {
		return err
	}
	// force=true: a channel still referenced by a policy is refused
	// otherwise, and destroy walks policies first anyway.
	if err := a.C.do(ctx, http.MethodDelete, a.C.url(monitoringHost, "/v3/%s?force=true", id), nil, nil); err != nil {
		if notFound(err) {
			return nil
		}
		return Classify(err, a.C.Project, "delete the notification channel", "roles/monitoring.notificationChannelEditor")
	}
	return nil
}

func (a *AlertChannels) idOf(ctx context.Context, name string) (string, error) {
	obs, err := a.List(ctx)
	if err != nil {
		return "", err
	}
	for _, o := range obs {
		if o.Name == name {
			return o.ID, nil
		}
	}
	return "", nil
}

// AlertPolicies owns alert policies.
type AlertPolicies struct {
	C      *Client
	App    string
	Env    string
	Labels map[string]string
	// Channels resolves a declared channel name to its live resource
	// name, because a policy references channels by resource name and
	// only the live listing knows them.
	Channels *AlertChannels
}

func (a *AlertPolicies) Kind() provision.Kind { return provision.KindAlertPolicy }

func (a *AlertPolicies) base() string {
	return a.C.url(monitoringHost, "/v3/projects/%s/alertPolicies", a.C.Project)
}

type alertPolicy struct {
	Name                 string            `json:"name,omitempty"`
	DisplayName          string            `json:"displayName,omitempty"`
	Combiner             string            `json:"combiner,omitempty"`
	Severity             string            `json:"severity,omitempty"`
	Conditions           []alertCondition  `json:"conditions,omitempty"`
	NotificationChannels []string          `json:"notificationChannels,omitempty"`
	UserLabels           map[string]string `json:"userLabels,omitempty"`
	Enabled              *bool             `json:"enabled,omitempty"`
	Documentation        *documentation    `json:"documentation,omitempty"`
}

// documentation is what the notification body carries. mimeType is the
// only value the API accepts; links are capped at 3 by the API and the
// display name at 63 characters, both enforced where they are built.
type documentation struct {
	Content  string    `json:"content,omitempty"`
	MimeType string    `json:"mimeType,omitempty"`
	Subject  string    `json:"subject,omitempty"`
	Links    []docLink `json:"links,omitempty"`
}

type docLink struct {
	DisplayName string `json:"displayName,omitempty"`
	URL         string `json:"url,omitempty"`
}

type alertCondition struct {
	DisplayName string      `json:"displayName,omitempty"`
	PromQL      *promQLCond `json:"conditionPrometheusQueryLanguage,omitempty"`
}

type promQLCond struct {
	Query              string `json:"query"`
	Duration           string `json:"duration,omitempty"`
	EvaluationInterval string `json:"evaluationInterval,omitempty"`
}

func (a *AlertPolicies) List(ctx context.Context) ([]provision.Observed, error) {
	var out struct {
		Policies []alertPolicy `json:"alertPolicies"`
	}
	if err := a.C.do(ctx, http.MethodGet, a.base(), nil, &out); err != nil {
		if disabledAPI(err) {
			return nil, nil
		}
		return nil, Classify(err, a.C.Project, "list alert policies", "roles/monitoring.alertPolicyEditor")
	}
	var observed []provision.Observed
	for _, p := range out.Policies {
		if !ownedBy(p.UserLabels, a.App, a.Env) {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind: provision.KindAlertPolicy, Name: unprefixed(p.DisplayName, a.Env),
			SpecHash: p.UserLabels["tako-spec-hash"], ID: p.Name,
		})
	}
	return observed, nil
}

func (a *AlertPolicies) body(ctx context.Context, o provision.Object) (alertPolicy, error) {
	labels := map[string]string{"tako-spec-hash": provision.SpecHash(o.Spec)}
	for k, v := range a.Labels {
		labels[k] = v
	}
	var chans []string
	for _, want := range strings.Split(fmt.Sprint(o.Spec["channels"]), ",") {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		id, err := a.Channels.idOf(ctx, want)
		if err != nil {
			return alertPolicy{}, err
		}
		if id == "" {
			// A policy with a dangling channel is a policy that pages
			// nobody: refuse rather than create a silent one.
			return alertPolicy{}, fmt.Errorf("alert %q names channel %q, which does not exist yet — channels reconcile before policies, so this is a tako ordering bug", o.Name, want)
		}
		chans = append(chans, id)
	}
	cond := promQLCond{
		Query:              fmt.Sprint(o.Spec["query"]),
		EvaluationInterval: fmt.Sprint(o.Spec["evaluationInterval"]),
	}
	// A retest window is refused past a 1d1h lookback, so the desired
	// spec says whether one is allowed and this never guesses.
	if d, ok := o.Spec["duration"].(string); ok && d != "" {
		cond.Duration = d
	}
	enabled := true
	condName := fmt.Sprint(o.Spec["conditionName"])
	if condName == "" || condName == "<nil>" {
		condName = o.Name
	}
	return alertPolicy{
		DisplayName:          fmt.Sprint(o.Spec["displayName"]),
		Combiner:             "OR",
		Severity:             fmt.Sprint(o.Spec["severity"]),
		Conditions:           []alertCondition{{DisplayName: condName, PromQL: &cond}},
		NotificationChannels: chans,
		UserLabels:           labels,
		Enabled:              &enabled,
		Documentation:        docOf(o),
	}, nil
}

// docOf renders the documentation half of the spec. Absent content
// means no documentation object at all rather than an empty one, so a
// policy tako has nothing useful to say about keeps Google's default.
func docOf(o provision.Object) *documentation {
	content, _ := o.Spec["docContent"].(string)
	if content == "" {
		return nil
	}
	d := &documentation{Content: content, MimeType: "text/markdown"}
	if s, ok := o.Spec["docSubject"].(string); ok {
		d.Subject = s
	}
	links, _ := o.Spec["docLinks"].([]any)
	for _, raw := range links {
		l, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		// The API caps links at 3 and refuses a display name past 63
		// characters; both are silent truncations here rather than a
		// failed deploy, because a missing link must never be the
		// reason an alert does not exist.
		if len(d.Links) == 3 {
			break
		}
		name := fmt.Sprint(l["displayName"])
		if len(name) > 63 {
			name = name[:63]
		}
		d.Links = append(d.Links, docLink{DisplayName: name, URL: fmt.Sprint(l["url"])})
	}
	return d
}

func (a *AlertPolicies) Create(ctx context.Context, o provision.Object) error {
	body, err := a.body(ctx, o)
	if err != nil {
		return err
	}
	if err := a.C.do(ctx, http.MethodPost, a.base(), body, nil); err != nil {
		return Classify(err, a.C.Project, "create the alert policy", "roles/monitoring.alertPolicyEditor")
	}
	return nil
}

func (a *AlertPolicies) Update(ctx context.Context, o provision.Object) error {
	if err := a.Delete(ctx, o.Name); err != nil {
		return err
	}
	return a.Create(ctx, o)
}

func (a *AlertPolicies) Delete(ctx context.Context, name string) error {
	obs, err := a.List(ctx)
	if err != nil {
		return err
	}
	for _, o := range obs {
		if o.Name != name {
			continue
		}
		if err := a.C.do(ctx, http.MethodDelete, a.C.url(monitoringHost, "/v3/%s", o.ID), nil, nil); err != nil {
			if notFound(err) {
				return nil
			}
			return Classify(err, a.C.Project, "delete the alert policy", "roles/monitoring.alertPolicyEditor")
		}
	}
	return nil
}

// ownedBy reports whether an object's user labels mark it as this app's
// and this env's. A foreign policy in the same project is never touched.
func ownedBy(labels map[string]string, app, env string) bool {
	return labels["tako-app"] == app && labels["tako-env"] == env
}

// unprefixed recovers a declared name from the deployed displayName by
// removing the env prefix Config.Name added. prod deploys bare names, so
// there is nothing to remove there.
func unprefixed(displayName, env string) string {
	if env == "" || env == "prod" {
		return displayName
	}
	return strings.TrimPrefix(displayName, env+"-")
}
