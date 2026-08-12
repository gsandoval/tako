package entitytag

// The oracle is encoding/json, not a second reading of its spec.
//
// tako indexes and queries a property by the name the DOCUMENT carries,
// and the document is whatever json.Marshal emits. So every case below
// builds a struct with the tag under test, marshals it, and asserts Name
// reports the key that actually came out. A rule derived from reading
// the docs would have agreed with itself and still been wrong — which is
// how json:"-," ended up spelled two ways (item 43).

import (
	"encoding/json"
	"reflect"
	"testing"
)

// marshaledKey is the truth: the single key json.Marshal emits for a
// one-field struct carrying rawTag, or "" when the field is dropped.
// The value is non-zero deliberately — with a zero one, omitempty drops
// the field and every tag carrying it would read as a skip.
func marshaledKey(t *testing.T, rawTag string) string {
	t.Helper()
	typ := reflect.StructOf([]reflect.StructField{{
		Name: "Field", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(rawTag),
	}})
	v := reflect.New(typ).Elem()
	v.Field(0).SetString("set")
	doc, err := json.Marshal(v.Interface())
	if err != nil {
		t.Fatalf("marshaling a struct tagged %q: %v", rawTag, err)
	}
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatalf("%s: %v", doc, err)
	}
	if len(m) > 1 {
		t.Fatalf("a one-field struct marshaled to %s", doc)
	}
	for k := range m {
		return k
	}
	return ""
}

var tagCases = []struct {
	raw  string
	note string
}{
	{``, "no tag at all"},
	{`json:"slug"`, "a plain rename"},
	{`json:"slug,omitempty"`, "a rename with an option"},
	{`json:",omitempty"`, "options only — the field keeps its Go name"},
	{`json:""`, "an empty tag"},
	{`json:"-"`, "the skip"},
	{`json:"-,"`, "NOT the skip: a field named literally -"},
	{`json:"-,omitempty"`, "also a field named -, with an option"},
	{`json:"a b"`, "a space is legal in a json name, surprisingly"},
	{`json:"a\"b"`, "a quote is not: encoding/json ignores the whole name"},
	{`json:"slug" entity:"index"`, "alongside an entity tag"},
	{`entity:"index"`, "an entity tag and no json tag"},
}

func TestNameMatchesWhatJSONActuallyEmits(t *testing.T) {
	for _, c := range tagCases {
		key := marshaledKey(t, c.raw)
		if key == "" {
			continue // the skip case; TestOmitted owns it
		}
		if got := Name(c.raw, "Field"); got != key {
			t.Errorf("Name(%q) = %q, but json.Marshal stores it under %q — %s.\n"+
				"An index on %q would match nothing, silently.", c.raw, got, key, c.note, got)
		}
	}
}

// The other half: a field encoding/json drops cannot be indexed, and
// Omitted is how both callers refuse it.
func TestOmittedMatchesWhatJSONActuallyDrops(t *testing.T) {
	for _, c := range tagCases {
		dropped := marshaledKey(t, c.raw) == ""
		if got := Omitted(c.raw); got != dropped {
			t.Errorf("Omitted(%q) = %v, but json.Marshal %s the field — %s", c.raw, got,
				map[bool]string{true: "drops", false: "stores"}[dropped], c.note)
		}
	}
}

func TestKindReadsTheEntityRole(t *testing.T) {
	for raw, want := range map[string]string{
		`entity:"id"`:                   "id",
		`entity:"index"`:                "index",
		`json:"slug" entity:"index"`:    "index",
		`entity:"index" json:"slug"`:    "index",
		`json:"slug"`:                   "",
		``:                              "",
		`entity:"id" json:",omitempty"`: "id",
	} {
		if got := Kind(raw); got != want {
			t.Errorf("Kind(%q) = %q, want %q", raw, got, want)
		}
	}
}
