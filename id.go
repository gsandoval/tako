package tako

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewID returns a new unique identifier, suitable for entity and object
// keys: 16 lowercase base32 characters of cryptographic randomness.
func NewID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("tako: NewID: " + err.Error()) // crypto/rand never fails on supported platforms
	}
	return strings.ToLower(idEncoding.EncodeToString(b[:]))
}
