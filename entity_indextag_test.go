package tako

import (
	"strings"
	"testing"
)

// CR8: an index on a field the document does not carry matches
// nothing, and says nothing. Both halves of the contradiction are
// spelled in the struct, so this is answerable where the tags are read.
type hiddenIndex struct {
	ID    string `entity:"id"`
	Owner string `entity:"index" json:"-"`
}

func TestAnIndexOnAFieldJSONDropsIsRefused(t *testing.T) {
	h := Entity[hiddenIndex]("cr8")
	_, err := h.indexedFields()
	if err == nil {
		t.Fatal(`entity:"index" with json:"-" was accepted — the write declares an index on a ` +
			"key no document has, and every query on it returns nothing without erroring")
	}
	for _, want := range []string{"Owner", `json:"-"`, "matches nothing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q: %v", want, err)
		}
	}
}

// A field merely RENAMED by a json tag is the ordinary case and must
// keep working: the index follows the marshaled name.
type renamedIndex struct {
	ID    string `entity:"id"`
	Owner string `entity:"index" json:"owner_id"`
}

func TestARenamedIndexUsesTheMarshaledName(t *testing.T) {
	h := Entity[renamedIndex]("cr8-ok")
	fields, err := h.indexedFields()
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0] != "owner_id" {
		t.Errorf("indexed fields = %v, want [owner_id]", fields)
	}
}

// Item 43: json:"-," is not the skip — encoding/json stores the field
// under the name "-". The schema used to read it as the Go field name
// while the analyzer read it as "-", which is the same silent no-match
// the refusal above exists to prevent, arrived at by disagreement rather
// than by contradiction. Both now read internal/entitytag.
type oddlyNamedIndex struct {
	ID    string `entity:"id"`
	Owner string `entity:"index" json:"-,"`
}

func TestAnIndexNamedDashFollowsEncodingJSON(t *testing.T) {
	h := Entity[oddlyNamedIndex]("item43")
	fields, err := h.indexedFields()
	if err != nil {
		t.Fatalf(`json:"-," was refused as if it were the skip: %v`, err)
	}
	if len(fields) != 1 || fields[0] != "-" {
		t.Errorf(`indexed fields = %v, want [-] — the name json.Marshal actually emits`, fields)
	}
}
