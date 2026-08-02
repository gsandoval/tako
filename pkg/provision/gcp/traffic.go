package gcp

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// Revision is one Cloud Run revision of a managed service — the unit
// canary/promote/rollback move traffic between. Digest-pinned images
// make a revision set a version; the annotations name its git SHA.
type Revision struct {
	Name    string
	Created time.Time
	GitSHA  string
	Ready   bool
}

// TrafficTarget is one slice of a service's traffic.
type TrafficTarget struct {
	Revision string // empty when Latest
	Percent  int
	Latest   bool
}

func (r *RunServices) serviceURL(name string) string {
	return r.C.url(runHost, "/v2/%s/services/%s", r.parent(), name)
}

// Revisions lists the service's revisions, newest first.
func (r *RunServices) Revisions(ctx context.Context, service string) ([]Revision, error) {
	u := r.serviceURL(service) + "/revisions?pageSize=100"
	var out struct {
		Revisions []struct {
			Name        string            `json:"name"`
			CreateTime  time.Time         `json:"createTime"`
			Annotations map[string]string `json:"annotations"`
			Conditions  []struct {
				Type  string `json:"type"`
				State string `json:"state"`
			} `json:"conditions"`
		} `json:"revisions"`
	}
	if err := r.C.do(ctx, http.MethodGet, u, nil, &out); err != nil {
		return nil, Classify(err, r.C.Project, "list revisions", "roles/run.viewer")
	}
	revs := make([]Revision, 0, len(out.Revisions))
	for _, rev := range out.Revisions {
		ready := false
		for _, c := range rev.Conditions {
			if c.Type == "Ready" && c.State == "CONDITION_SUCCEEDED" {
				ready = true
			}
		}
		revs = append(revs, Revision{
			Name:    lastSegment(rev.Name),
			Created: rev.CreateTime,
			GitSHA:  rev.Annotations["tako-git-sha"],
			Ready:   ready,
		})
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].Created.After(revs[j].Created) })
	return revs, nil
}

// Traffic reads the service's ACTUAL serving split (trafficStatuses:
// resolved revision names, not intent).
func (r *RunServices) Traffic(ctx context.Context, service string) ([]TrafficTarget, error) {
	var svc struct {
		TrafficStatuses []struct {
			Type     string `json:"type"`
			Revision string `json:"revision"`
			Percent  int    `json:"percent"`
		} `json:"trafficStatuses"`
	}
	if err := r.C.do(ctx, http.MethodGet, r.serviceURL(service), nil, &svc); err != nil {
		return nil, Classify(err, r.C.Project, "read the service's traffic", "roles/run.viewer")
	}
	var targets []TrafficTarget
	for _, t := range svc.TrafficStatuses {
		targets = append(targets, TrafficTarget{
			Revision: lastSegment(t.Revision),
			Percent:  t.Percent,
			Latest:   t.Type == "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST",
		})
	}
	return targets, nil
}

func trafficBody(targets []TrafficTarget) []map[string]any {
	out := make([]map[string]any, 0, len(targets))
	for _, t := range targets {
		m := map[string]any{"percent": t.Percent}
		if t.Latest {
			m["type"] = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
		} else {
			m["type"] = "TRAFFIC_TARGET_ALLOCATION_TYPE_REVISION"
			m["revision"] = t.Revision
		}
		out = append(out, m)
	}
	return out
}

// SetTraffic moves the service's split — the whole of promote and
// rollback; never a rebuild.
func (r *RunServices) SetTraffic(ctx context.Context, service string, targets []TrafficTarget) error {
	body := map[string]any{"traffic": trafficBody(targets)}
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodPatch, r.serviceURL(service)+"?updateMask=traffic", body, &op); err != nil {
		return Classify(err, r.C.Project, "shift the service's traffic", "roles/run.admin")
	}
	return r.C.waitOperation(ctx, r.C.url(runHost, "/v2/"+op.Name), waitLong)
}

// serving is the revision currently carrying the most traffic ("" when
// the service has never served). LATEST-type traffic statuses carry no
// revision name in the v2 API, so an all-latest service resolves to its
// newest ready revision — which, before any new rollout, IS the one
// serving.
func (r *RunServices) serving(ctx context.Context, service string) (string, error) {
	targets, err := r.Traffic(ctx, service)
	if err != nil {
		return "", err
	}
	best, pct := "", -1
	for _, t := range targets {
		if t.Percent > pct && t.Revision != "" {
			best, pct = t.Revision, t.Percent
		}
	}
	if best != "" {
		return best, nil
	}
	revs, err := r.Revisions(ctx, service)
	if err != nil {
		return "", err
	}
	for _, rev := range revs {
		if rev.Ready {
			return rev.Name, nil
		}
	}
	return "", nil
}

// Promote sends 100% of traffic to the latest ready revision.
func (r *RunServices) Promote(ctx context.Context, service string) error {
	return r.SetTraffic(ctx, service, []TrafficTarget{{Latest: true, Percent: 100}})
}

// Rollback shifts 100% of traffic to the revision before the one
// serving now, returning (from, to). No rebuild: the images and
// revisions already exist. Infra-shape changes roll back via git
// revert + tako deploy instead.
func (r *RunServices) Rollback(ctx context.Context, service string) (from, to Revision, err error) {
	revs, err := r.Revisions(ctx, service)
	if err != nil {
		return from, to, err
	}
	current, err := r.serving(ctx, service)
	if err != nil {
		return from, to, err
	}
	idx := -1
	for i, rev := range revs {
		if rev.Name == current || (current == "" && i == 0) {
			from, idx = rev, i
			break
		}
	}
	for i := idx + 1; i >= 0 && i < len(revs); i++ {
		if revs[i].Ready {
			to = revs[i]
			break
		}
	}
	if to.Name == "" {
		return from, to, fmt.Errorf("nothing to roll back to — %s has a single revision", service)
	}
	if err := r.SetTraffic(ctx, service, []TrafficTarget{{Revision: to.Name, Percent: 100}}); err != nil {
		return from, to, err
	}
	return from, to, nil
}
