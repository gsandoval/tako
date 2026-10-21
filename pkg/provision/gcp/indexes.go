package gcp

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/provision"
)

// DatastoreIndexes manages derived composite indexes
// (KindDatastoreIndex). Indexes carry no labels or descriptions, so
// ownership is content-addressed: a live index matching a desired
// object's (kind, properties) IS that object; foreign indexes are left
// alone and never reported. Indexes are immutable — a spec change is a
// new identity, so Update never fires.
type DatastoreIndexes struct {
	C       *Client
	Desired []provision.Object
	// Kinds are the app's declared entity kinds: live indexes on them
	// that match NO desired object are ORPHANS (schema churn leaves
	// them behind — found live in the W11 walk). Indexes on foreign
	// kinds stay invisible, as before.
	Kinds []string
	Say   func(format string, args ...any)

	orphanIDs map[string]string // orphan name → live indexId, filled by List
}

// liveName renders a live index the way desired names read, so plans
// and prune lines look identical for both.
func liveName(li liveIndex) string {
	props := make([]string, 0, len(li.Properties))
	for _, p := range li.Properties {
		name := p.Name
		if p.Direction == "DESCENDING" {
			name = "-" + name
		}
		props = append(props, name)
	}
	return li.Kind + " (" + strings.Join(props, ", ") + ")"
}

func (d *DatastoreIndexes) Kind() provision.Kind { return provision.KindDatastoreIndex }

type indexProp struct {
	Name      string `json:"name"`
	Direction string `json:"direction"`
}

type liveIndex struct {
	IndexID    string      `json:"indexId"`
	Kind       string      `json:"kind"`
	Properties []indexProp `json:"properties"`
	State      string      `json:"state"`
}

func specProps(o provision.Object) []indexProp {
	raw, _ := o.Spec["properties"].([]any)
	props := make([]indexProp, 0, len(raw))
	for _, p := range raw {
		m, _ := p.(map[string]any)
		props = append(props, indexProp{Name: fmt.Sprint(m["name"]), Direction: fmt.Sprint(m["direction"])})
	}
	return props
}

func sameIndex(o provision.Object, live liveIndex) bool {
	if fmt.Sprint(o.Spec["entityKind"]) != live.Kind {
		return false
	}
	props := specProps(o)
	if len(props) != len(live.Properties) {
		return false
	}
	for i := range props {
		if props[i] != live.Properties[i] {
			return false
		}
	}
	return true
}

func (d *DatastoreIndexes) live(ctx context.Context) ([]liveIndex, error) {
	u := d.C.url(datastoreHost, "/v1/projects/"+d.C.Project+"/indexes")
	var out struct {
		Indexes []liveIndex `json:"indexes"`
	}
	if err := d.C.do(ctx, http.MethodGet, u, nil, &out); err != nil {
		if disabledAPI(err) {
			return nil, nil
		}
		return nil, Classify(err, d.C.Project, "list datastore indexes", "roles/datastore.indexAdmin")
	}
	return out.Indexes, nil
}

func (d *DatastoreIndexes) List(ctx context.Context) ([]provision.Observed, error) {
	hasIndexes := false
	for _, o := range d.Desired {
		if o.Kind == provision.KindDatastoreIndex {
			hasIndexes = true
			break
		}
	}
	if !hasIndexes && len(d.Kinds) == 0 {
		return nil, nil
	}
	live, err := d.live(ctx)
	if err != nil {
		return nil, err
	}
	managedKind := map[string]bool{}
	for _, k := range d.Kinds {
		managedKind[k] = true
	}
	d.orphanIDs = map[string]string{}
	var observed []provision.Observed
	for _, li := range live {
		if li.State == "ERROR" || li.State == "DELETING" { // a dying index is absent
			continue
		}
		matched := false
		for _, o := range d.Desired {
			if sameIndex(o, li) {
				observed = append(observed, provision.Observed{
					Kind:     provision.KindDatastoreIndex,
					Name:     o.Name,
					SpecHash: provision.SpecHash(o.Spec),
				})
				matched = true
				break
			}
		}
		if !matched && managedKind[li.Kind] {
			// Schema churn's leftovers: an index on a kind tako manages
			// that no current query shape wants — surfaced as an orphan
			// (kept), deletable by prune (indexes are derivable, never
			// data).
			name := liveName(li)
			d.orphanIDs[name] = li.IndexID
			observed = append(observed, provision.Observed{
				Kind:     provision.KindDatastoreIndex,
				Name:     name,
				SpecHash: "orphan",
			})
		}
	}
	return observed, nil
}

// Create STARTS a build and returns; Wait is what blocks. Datastore
// builds indexes concurrently, so waiting inside Create made N indexes
// cost N × ~6 minutes for work the server was willing to overlap.
func (d *DatastoreIndexes) Create(ctx context.Context, o provision.Object) error {
	u := d.C.url(datastoreHost, "/v1/projects/"+d.C.Project+"/indexes")
	body := map[string]any{
		"kind":       o.Spec["entityKind"],
		"ancestor":   "NONE",
		"properties": specProps(o),
	}
	var op struct {
		Name string `json:"name"`
	}
	if err := d.C.do(ctx, http.MethodPost, u, body, &op); err != nil {
		return Classify(err, d.C.Project, "create the datastore index", "roles/datastore.indexAdmin")
	}
	if d.Say != nil {
		d.Say("index        %s building…", o.Name)
	}
	return nil
}

// indexWait is how long Wait gives a build. Measured: ~4m to serving on
// an empty database, and it does not grow much with data at this scale.
const indexWait = 15 * time.Minute

// Wait blocks until every desired index is SERVING.
//
// It waits on the index's own state, not on the create operation. The
// two disagree by a lot: measured on an empty database, the index
// reaches READY at ~4m08s and the operation only reports done at
// ~5m50s — 1m42s of waiting after queries already work, which is
// exactly the "the console says it's done" complaint.
//
// It waits for every DESIRED index rather than only the ones this run
// created, so a deploy resumed while an earlier build was still running
// still refuses to put services in front of an index that cannot serve
// them. On a converged deploy this is one list call.
func (d *DatastoreIndexes) Wait(ctx context.Context) error {
	if len(d.Desired) == 0 {
		return nil
	}
	waiting := map[string]provision.Object{}
	for _, o := range d.Desired {
		if o.Kind == provision.KindDatastoreIndex {
			waiting[o.Name] = o
		}
	}
	if len(waiting) == 0 {
		return nil
	}
	start := time.Now()
	deadline := start.Add(indexWait)
	announced := false
	for {
		live, err := d.live(ctx)
		if err != nil {
			return err
		}
		for name, o := range waiting {
			for _, li := range live {
				if !sameIndex(o, li) {
					continue
				}
				switch li.State {
				case "READY":
					if announced && d.Say != nil {
						d.Say("index        %s ready (%s)", name, time.Since(start).Round(time.Second))
					}
					delete(waiting, name)
				case "ERROR":
					return fmt.Errorf("datastore index %s failed to build — delete it and re-deploy: gcloud datastore indexes cleanup, or remove index %s in the console", name, li.IndexID)
				}
				break
			}
		}
		if len(waiting) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			var names []string
			for name := range waiting {
				names = append(names, name)
			}
			sort.Strings(names)
			return fmt.Errorf("datastore index %s did not finish building within %s — it is still building, so re-running tako deploy will pick it up where it is",
				strings.Join(names, ", "), indexWait)
		}
		if !announced && d.Say != nil {
			// Only once, and only if we are actually going to wait: a
			// converged deploy should say nothing at all here.
			d.Say("index        waiting for %d build(s) — Datastore takes ~4 minutes per index", len(waiting))
			announced = true
		}
		time.Sleep(5 * time.Second)
	}
}

func (d *DatastoreIndexes) Update(ctx context.Context, o provision.Object) error {
	return nil // content-addressed: matching hash is the only update state
}

func (d *DatastoreIndexes) Delete(ctx context.Context, name string) error {
	if id, ok := d.orphanIDs[name]; ok {
		u := d.C.url(datastoreHost, "/v1/projects/"+d.C.Project+"/indexes/"+id)
		if err := d.C.do(ctx, http.MethodDelete, u, nil, nil); err != nil {
			return Classify(err, d.C.Project, "delete the datastore index", "roles/datastore.indexAdmin")
		}
		return nil
	}
	for _, o := range d.Desired {
		if o.Name != name {
			continue
		}
		live, err := d.live(ctx)
		if err != nil {
			return err
		}
		for _, li := range live {
			if sameIndex(o, li) {
				u := d.C.url(datastoreHost, "/v1/projects/"+d.C.Project+"/indexes/"+li.IndexID)
				if err := d.C.do(ctx, http.MethodDelete, u, nil, nil); err != nil {
					return Classify(err, d.C.Project, "delete the datastore index", "roles/datastore.indexAdmin")
				}
				return nil
			}
		}
	}
	return nil
}
