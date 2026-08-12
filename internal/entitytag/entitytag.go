// Package entitytag is the one reading of an entity struct's tags.
//
// Two places need it and they cannot share a type: the analyzer derives
// composite indexes from go/types, and the runtime writes documents
// through reflect. What they must never disagree about is what a tag
// MEANS — which property name a field is stored under, and whether it is
// stored at all — because a disagreement is silent. An index declared on
// one property name while the document carries another matches nothing,
// and Datastore answers an empty result without erroring.
//
// Discovered item 43 filed the two derivations as two sources for one
// rule. They had already drifted: json:"-," names a field literally "-"
// (encoding/json's own rule), which the analyzer got right and the
// runtime got wrong, spelling it as the Go field name.
//
// Both callers hold the raw struct tag string, so that is the input.
// encoding/json is the oracle — see the test, which asserts Name against
// what json.Marshal actually emits rather than against a second reading
// of the same spec.
package entitytag

import (
	"reflect"
	"strings"
	"unicode"
)

// Kind returns the entity role a field declares: "id", "index", or ""
// for a field tako does not treat specially.
func Kind(rawTag string) string {
	return reflect.StructTag(rawTag).Get("entity")
}

// Omitted reports whether encoding/json leaves the field out of the
// document entirely — json:"-", and only that spelling. json:"-," names
// a field "-" and IS stored.
func Omitted(rawTag string) bool {
	tag, ok := reflect.StructTag(rawTag).Lookup("json")
	return ok && tag == "-"
}

// Name returns the property name encoding/json marshals the field
// under: the json tag's name where it gives a usable one, the Go field
// name otherwise. Callers that care whether the field is stored at all
// ask Omitted first — for json:"-" this returns the field name, which is
// the name nothing is stored under.
func Name(rawTag, field string) string {
	tag, ok := reflect.StructTag(rawTag).Lookup("json")
	if !ok {
		return field
	}
	name, _, _ := strings.Cut(tag, ",")
	// A name encoding/json will not accept is ignored by encoding/json,
	// field name and all — so it is ignored here too, or tako would
	// index a property no document has. "" covers both json:"" and
	// json:",omitempty".
	if name == "" || !validName(name) {
		return field
	}
	return name
}

// validName mirrors encoding/json's isValidTag, character set included:
// most punctuation is allowed in a name (a space is), while a quote or a
// backslash disqualifies it. Kept as a mirror rather than an
// approximation because the two only have to agree at the edges; the
// test holds them to it.
func validName(s string) bool {
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
			// Backslash and quote are reserved by their absence here, and
			// the comma never reaches this — Cut ended the name at one.
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}
