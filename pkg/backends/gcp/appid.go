package gcp

// The app's two names.
//
// A tako app is NAMED by its Go module path, and that name is what a
// person reads: the deploy banner, an alert's documentation, a service
// account's description. Google never validates any of those.
//
// Google does validate the places tako stores the app as a KEY — label
// values on eleven resource kinds, the app segment of a bucket name,
// the lease object, the metric namespace — and its rules there are
// narrow: GCS admits `[\p{Ll}\p{Lo}\p{N}_-]{0,63}` in a label value
// and rejects the dot in `github.com/gsandoval/flauta` outright. Until
// this file existed tako sent the module path to all of them, which
// meant the first create of the first deploy failed for every app
// whose module path names a host — the normal case, and one no example
// could reach, because every example declares a bare module name.
//
// AppID is that key. The module path stays the name.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// maxAppID bounds the id. A bucket name is capped at 63 characters and
// already spends a project id, an env prefix and the declared bucket
// name; 30 is what leaves room for realistic values of the other three
// (preflight checks the assembled name, which is the honest guard).
const maxAppID = 30

// headAppID is how much of a derived id is the readable head. The
// remainder is a hyphen and six hex digits of hash, so head + 7 fits
// maxAppID with a character to spare.
const headAppID = 22

// AppID is the app's machine identifier: what tako asks Google to
// store wherever Google constrains the characters.
//
// A module path that is already a legal id IS its own id. That is the
// load-bearing half: labels are the observed state (Config.Labels),
// so a derivation that moved `hello` to something else would orphan
// every object of every app already deployed. Every example module —
// blog, gallery, hello, journal, shop, shortlink, siren — is legal and
// unchanged.
//
// Anything else derives from its last path segment plus a hash of the
// whole path: `github.com/gsandoval/flauta` → `flauta-a70a87`. Short
// enough for a bucket segment, readable enough to recognise in a
// console, unique because the hash covers exactly what truncation
// dropped, and stable because both halves are pure functions of the
// module path in go.mod.
//
// The result is always legal, so AppID is idempotent — which is what
// lets a deploy write it into TAKO_APP and the runtime hand that value
// straight back to BucketName.
func AppID(module string) string {
	if legalAppID(module) {
		return module
	}
	seg := module
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	seg = sanitizeAppID(seg)
	if len(seg) > headAppID {
		seg = strings.Trim(seg[:headAppID], "-_")
	}
	if seg == "" {
		// Nothing survived — `example.com/...`. The hash alone is a
		// legal id, but one that starts with a hyphen is not, and a
		// bare hex string reads like a mistake in a plan.
		seg = "app"
	}
	sum := sha256.Sum256([]byte(module))
	return seg + "-" + hex.EncodeToString(sum[:3])
}

// legalAppID reports whether s can be stored as-is: the intersection of
// what a GCS/Run/Pub-Sub label value and a bucket-name segment accept,
// plus the length bound. Lowercase letters, digits, hyphen and
// underscore, starting with a letter or digit.
func legalAppID(s string) bool {
	if s == "" || len(s) > maxAppID {
		return false
	}
	if c := s[0]; !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') {
		return false
	}
	for _, r := range s {
		if !idRune(r) {
			return false
		}
	}
	return true
}

// sanitizeAppID lowercases, replaces every rune Google will not take
// with a hyphen, collapses runs of them, and trims the ends.
func sanitizeAppID(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if idRune(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-_")
}

func idRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}
