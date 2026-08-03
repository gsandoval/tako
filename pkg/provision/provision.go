// Package provision is the stateless reconciler (spec §4.6/§4.6a):
// desired state comes from the AppGraph, observed state from listing
// GCP objects by tako labels, and the diff is spec-hash comparison —
// absent→create, hash differs→update, matches→skip. There is no state
// file; the cloud is the state.
package provision

import (
	"context"
	"fmt"
	"sort"
)

// Kind enumerates the managed object kinds, in reconcile order.
type Kind string

const (
	KindRepo               Kind = "artifact repo"
	KindServiceAccount     Kind = "service account"
	KindBucket             Kind = "bucket"
	KindDatastoreIndex     Kind = "datastore index"
	KindTopic              Kind = "topic"
	KindBucketNotification Kind = "bucket notification"
	KindPullSubscription   Kind = "pull subscription"
	KindTasksQueue         Kind = "tasks queue"
	KindRunService         Kind = "run service"
	KindRunJob             Kind = "run job"
	KindIamGrants          Kind = "iam grants"
	KindPushSubscription   Kind = "push subscription"
	KindSchedulerJob       Kind = "scheduler job"
	KindDomainMapping      Kind = "domain mapping"
	KindAlertChannel       Kind = "alert channel"
	KindAlertPolicy        Kind = "alert policy"
)

// reconcileOrder is additive-first (spec §4.6a): supporting objects
// before the services that use them; pull (dead-letter keep)
// subscriptions right after their topics so no dead-letter is ever
// published into a subscriber-less topic (Pub/Sub drops those); grants
// after services (their targets must exist) and push subscriptions last
// (they deliver into services whose IAM must already admit the push
// identity); domain mappings last of all, because a mapping names a live
// run service as its route. Destroy walks it backwards.
var reconcileOrder = []Kind{
	KindRepo, KindServiceAccount, KindBucket, KindDatastoreIndex, KindTopic,
	KindBucketNotification, KindPullSubscription, KindTasksQueue, KindRunService, KindRunJob,
	KindIamGrants, KindPushSubscription, KindSchedulerJob, KindDomainMapping,
	// Channels before policies: a policy names its channels by resource
	// name, so they must exist first. Both come last — they observe the
	// app rather than serve it, and a half-built app with alerts already
	// watching it would page on its own deploy.
	KindAlertChannel, KindAlertPolicy,
}

// DeleteOrder is reconcile order reversed — the order destroy walks.
func DeleteOrder() []Kind {
	out := make([]Kind, len(reconcileOrder))
	for i, k := range reconcileOrder {
		out[len(reconcileOrder)-1-i] = k
	}
	return out
}

// Object is one managed cloud object: identity, tako labels, and the
// spec tako intends (hashed for diffing).
type Object struct {
	Kind Kind
	// Name is the object's short name (repo name, SA account id, run
	// service name).
	Name string
	// Spec is the canonical intended spec; its hash is the diff unit.
	Spec map[string]any
}

// Observed is one live object as listed by label.
type Observed struct {
	Kind Kind
	Name string
	// ID is the API's own resource name, when the object's identity and
	// its address differ. Alert policies and notification channels are
	// created with a server-assigned id, so a delete has to look up what
	// a list told it rather than construct a path from the name.
	ID string
	// SpecHash is the tako-spec-hash recorded on the object; empty when
	// the object predates tako or lost its annotation.
	SpecHash string
	// LiveHash, when the client can compute it, is the hash of the
	// tako-owned fields as they are NOW — differing from SpecHash means
	// the object was edited outside tako (drift).
	LiveHash string
	// Detail carries the few live fields change classification needs
	// (a push subscription's topic — its immutable field). Kind-specific
	// and optional.
	Detail map[string]string
}

// Action classifies one plan entry.
type Action string

const (
	Create Action = "create"
	Update Action = "update"
	Skip   Action = "skip"
	Orphan Action = "orphaned (kept)"
)

// Class is a change's §4.6a update class. Creates are additive and
// updates in-place unless the diff touches an immutable field — then
// the apply is a replace (delete + create) and the plan says so before
// anything happens.
type Class string

const (
	ClassAdditive Class = "additive"
	ClassInPlace  Class = "in-place"
	ClassReplace  Class = "replace"
)

// Entry is one line of the plan.
type Entry struct {
	Action Action
	Kind   Kind
	Name   string
	// Class is the change's update class; Reason says why when it is
	// ClassReplace (the consequence the user must see).
	Class  Class
	Reason string
	// Suspect names a same-kind created object when this entry is an
	// orphan — the §4.6a rename suspicion: data does not follow a
	// rename automatically.
	Suspect string
	// Drift marks an object whose live state no longer matches the hash
	// it recorded — edited outside tako; tako-owned fields will be
	// overwritten on apply.
	Drift bool
}

// Plan is the classified diff for one deploy.
type Plan struct {
	App     string
	Env     string
	Entries []Entry
}

// Compute diffs desired against observed. Both the entries and their
// order are deterministic: reconcile order, then desired order.
func Compute(app, env string, desired []Object, observed []Observed) Plan {
	byKey := map[string]Observed{}
	seen := map[string]bool{}
	for _, o := range observed {
		byKey[string(o.Kind)+"/"+o.Name] = o
	}
	plan := Plan{App: app, Env: env}
	for _, kind := range reconcileOrder {
		for _, d := range desired {
			if d.Kind != kind {
				continue
			}
			key := string(d.Kind) + "/" + d.Name
			seen[key] = true
			live, exists := byKey[key]
			switch {
			case !exists:
				plan.Entries = append(plan.Entries, Entry{Action: Create, Kind: d.Kind, Name: d.Name, Class: ClassAdditive})
			case live.SpecHash != SpecHash(d.Spec):
				class, reason := classify(d, live)
				plan.Entries = append(plan.Entries, Entry{Action: Update, Kind: d.Kind, Name: d.Name,
					Class: class, Reason: reason, Drift: drifted(live)})
			case drifted(live):
				// The record matches the intent but the live object does
				// not: a console edit. The warning promises an overwrite,
				// so the plan applies one — a skip would let drift stand.
				plan.Entries = append(plan.Entries, Entry{Action: Update, Kind: d.Kind, Name: d.Name,
					Class: ClassInPlace, Drift: true})
			default:
				plan.Entries = append(plan.Entries, Entry{Action: Skip, Kind: d.Kind, Name: d.Name})
			}
		}
	}
	// Rename suspicion (§4.6a): a same-kind orphan + create pair looks
	// exactly like a rename, and data never follows one automatically.
	createdOfKind := map[Kind]string{}
	for _, e := range plan.Entries {
		if e.Action == Create {
			createdOfKind[e.Kind] = e.Name
		}
	}
	var orphans []Entry
	for _, o := range observed {
		if !seen[string(o.Kind)+"/"+o.Name] {
			orphans = append(orphans, Entry{Action: Orphan, Kind: o.Kind, Name: o.Name,
				Suspect: createdOfKind[o.Kind]})
		}
	}
	// Deterministic plans regardless of listing order.
	sort.Slice(orphans, func(i, j int) bool {
		if orphans[i].Kind != orphans[j].Kind {
			return kindRank(orphans[i].Kind) < kindRank(orphans[j].Kind)
		}
		return orphans[i].Name < orphans[j].Name
	})
	plan.Entries = append(plan.Entries, orphans...)
	return plan
}

func kindRank(k Kind) int {
	for i, kind := range reconcileOrder {
		if kind == k {
			return i
		}
	}
	return len(reconcileOrder)
}

func drifted(o Observed) bool {
	return o.LiveHash != "" && o.SpecHash != "" && o.LiveHash != o.SpecHash
}

// classify decides an update's §4.6a class from the one live detail
// that matters per kind: a push subscription's topic is immutable, so
// changing it is a replace (delete + create) with a consequence worth
// stating; everything else tako updates is in-place.
func classify(d Object, live Observed) (Class, string) {
	if d.Kind == KindPushSubscription {
		if want := fmt.Sprint(d.Spec["topic"]); live.Detail["topic"] != "" && live.Detail["topic"] != want {
			return ClassReplace, fmt.Sprintf("topic is immutable (%s → %s) — delete+create; in-flight messages on the old subscription are lost",
				live.Detail["topic"], want)
		}
	}
	if d.Kind == KindDomainMapping {
		if want := fmt.Sprint(d.Spec["routeName"]); live.Detail["routeName"] != "" && live.Detail["routeName"] != want {
			return ClassReplace, fmt.Sprintf("the mapped service is immutable (%s → %s) — delete+create; the domain stops answering until the new certificate provisions, which can take up to 24 hours",
				live.Detail["routeName"], want)
		}
	}
	return ClassInPlace, ""
}

// Counts sums the plan by action.
func (p Plan) Counts() (creates, updates, skips, orphans int) {
	for _, e := range p.Entries {
		switch e.Action {
		case Create:
			creates++
		case Update:
			updates++
		case Skip:
			skips++
		case Orphan:
			orphans++
		}
	}
	return
}

// Changed reports whether applying the plan would mutate anything.
func (p Plan) Changed() bool {
	c, u, _, _ := p.Counts()
	return c+u > 0
}

// NeedsConfirm reports whether the plan holds anything beyond
// additive/in-place changes — replaces or rename suspects — which the
// default deploy confirms interactively (§4.6a "plan before apply").
func (p Plan) NeedsConfirm() bool {
	for _, e := range p.Entries {
		if e.Class == ClassReplace || (e.Action == Orphan && e.Suspect != "") {
			return true
		}
	}
	return false
}

// marker is the plan line's change-class glyph: + additive, ~ in-place,
// ! replace, ? orphan, space for skips.
func marker(e Entry) string {
	switch {
	case e.Class == ClassReplace:
		return "!"
	case e.Action == Create:
		return "+"
	case e.Action == Update:
		return "~"
	case e.Action == Orphan:
		return "?"
	}
	return " "
}

// Render prints the plan the way the deploy banner shows it.
func (p Plan) Render() string {
	out := ""
	for _, e := range p.Entries {
		line := fmt.Sprintf("%s %-7s %s %s", marker(e), e.Action, e.Kind, e.Name)
		if e.Action == Orphan {
			line = fmt.Sprintf("%s %s %s %s", marker(e), e.Action, e.Kind, e.Name)
		}
		if e.Class == ClassReplace {
			line += "  ← REPLACE: " + e.Reason
		}
		if e.Drift {
			line += "  ← DRIFT: modified outside tako; tako-owned fields will be overwritten"
		}
		out += "  " + line + "\n"
		if e.Action == Orphan && e.Suspect != "" {
			warn := fmt.Sprintf("  ↳ rename suspect: %s %s appeared — if this is a rename, data does not follow automatically", e.Kind, e.Suspect)
			if e.Kind == KindBucket {
				warn += "; keep both declared, copy the objects, then drop the old (a migrating tako rename is a planned feature)"
			}
			out += "  " + warn + "\n"
		}
	}
	c, u, s, o := p.Counts()
	out += fmt.Sprintf("  %d creates, %d updates, %d skips, %d orphans\n", c, u, s, o)
	return out
}

// Applier executes plan entries; the gcp package implements it, tests
// use fakes.
type Applier interface {
	Create(ctx context.Context, obj Object) error
	Update(ctx context.Context, obj Object) error
}

// KindApplier is an Applier that owns one object kind (the gcp package
// provides one per kind).
type KindApplier interface {
	Kind() Kind
	Create(ctx context.Context, obj Object) error
	Update(ctx context.Context, obj Object) error
}

// Route dispatches apply calls to the applier owning each object's kind.
func Route(appliers ...KindApplier) Applier {
	m := map[Kind]KindApplier{}
	for _, a := range appliers {
		m[a.Kind()] = a
	}
	return routed(m)
}

type routed map[Kind]KindApplier

func (r routed) Create(ctx context.Context, obj Object) error {
	a, ok := r[obj.Kind]
	if !ok {
		return fmt.Errorf("no applier for kind %q", obj.Kind)
	}
	return a.Create(ctx, obj)
}

func (r routed) Update(ctx context.Context, obj Object) error {
	a, ok := r[obj.Kind]
	if !ok {
		return fmt.Errorf("no applier for kind %q", obj.Kind)
	}
	return a.Update(ctx, obj)
}

// Filter keeps only the plan entries whose kind is in kinds — deploys
// interleave non-plan work (image pushes) between kinds.
func (p Plan) Filter(kinds ...Kind) Plan {
	keep := map[Kind]bool{}
	for _, k := range kinds {
		keep[k] = true
	}
	out := Plan{App: p.App, Env: p.Env}
	for _, e := range p.Entries {
		if keep[e.Kind] {
			out.Entries = append(out.Entries, e)
		}
	}
	return out
}

// Apply walks the plan in order, creating and updating; skips and
// orphans are untouched (orphan cleanup is tako prune's job, and data
// destruction is never automatic).
func Apply(ctx context.Context, plan Plan, desired []Object, a Applier) error {
	byKey := map[string]Object{}
	for _, d := range desired {
		byKey[string(d.Kind)+"/"+d.Name] = d
	}
	for _, e := range plan.Entries {
		obj := byKey[string(e.Kind)+"/"+e.Name]
		switch e.Action {
		case Create:
			if err := a.Create(ctx, obj); err != nil {
				return fmt.Errorf("create %s %s: %w", e.Kind, e.Name, err)
			}
		case Update:
			if err := a.Update(ctx, obj); err != nil {
				return fmt.Errorf("update %s %s: %w", e.Kind, e.Name, err)
			}
		}
	}
	return nil
}
