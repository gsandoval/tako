package gcp

import (
	"context"
	"fmt"
	"net/http"
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
	Say     func(format string, args ...any)
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
	u := "https://datastore.googleapis.com/v1/projects/" + d.C.Project + "/indexes"
	var out struct {
		Indexes []liveIndex `json:"indexes"`
	}
	if err := d.C.do(ctx, http.MethodGet, u, nil, &out); err != nil {
		return nil, Classify(err, d.C.Project, "list datastore indexes", "roles/datastore.indexAdmin")
	}
	return out.Indexes, nil
}

func (d *DatastoreIndexes) List(ctx context.Context) ([]provision.Observed, error) {
	if len(d.Desired) == 0 {
		return nil, nil
	}
	live, err := d.live(ctx)
	if err != nil {
		return nil, err
	}
	var observed []provision.Observed
	for _, o := range d.Desired {
		for _, li := range live {
			if sameIndex(o, li) && li.State != "ERROR" {
				observed = append(observed, provision.Observed{
					Kind:     provision.KindDatastoreIndex,
					Name:     o.Name,
					SpecHash: provision.SpecHash(o.Spec),
				})
				break
			}
		}
	}
	return observed, nil
}

func (d *DatastoreIndexes) Create(ctx context.Context, o provision.Object) error {
	u := "https://datastore.googleapis.com/v1/projects/" + d.C.Project + "/indexes"
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
	// Index builds routinely outrun the generic operation wait — give
	// them their own ceiling (empty databases usually finish in ~5m).
	return d.C.waitOperation(ctx, "https://datastore.googleapis.com/v1/"+op.Name, 10*time.Minute)
}

func (d *DatastoreIndexes) Update(ctx context.Context, o provision.Object) error {
	return nil // content-addressed: matching hash is the only update state
}

func (d *DatastoreIndexes) Delete(ctx context.Context, name string) error {
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
				u := "https://datastore.googleapis.com/v1/projects/" + d.C.Project + "/indexes/" + li.IndexID
				if err := d.C.do(ctx, http.MethodDelete, u, nil, nil); err != nil {
					return Classify(err, d.C.Project, "delete the datastore index", "roles/datastore.indexAdmin")
				}
				return nil
			}
		}
	}
	return nil
}
