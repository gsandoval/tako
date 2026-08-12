package entityindextag

import "github.com/gsandoval/tako"

// Item 43: the analyzer derives composite indexes from the tags and the
// SDK writes documents from the same tags, and json:"-" makes the two
// disagree about a field that is not in the document at all. The SDK
// refuses it at first use; the analyzer refuses it here, with a file and
// a line, before a deploy provisions an index on a property no document
// carries.
var links = tako.Entity[Link]("links")

type Link struct {
	Slug   string `entity:"id"`
	Secret string `entity:"index" json:"-"`
	// json:"-," is NOT the skip — it names the property "-", so this one
	// is legal and is indexed under that name.
	Odd string `entity:"index" json:"-,"`
}

var List = tako.API("GET /api/links", list)

func list(ctx tako.Ctx, _ struct{}) ([]Link, error) {
	return links.Query(ctx).Where("Odd", "=", "x").All()
}
