package provision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SpecHash is the change-detection unit (spec §4.6a): a sha256 over the
// canonical form of the spec tako intends. Canonical = keys sorted at
// every level, JSON-encoded — map iteration order can never flap a
// deploy. The value is bare hex so it is legal everywhere it must live
// (GCP label values forbid ':').
func SpecHash(spec map[string]any) string {
	h := sha256.New()
	writeCanonical(h.Write, spec)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func writeCanonical(write func([]byte) (int, error), v any) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		write([]byte("{"))
		for i, k := range keys {
			if i > 0 {
				write([]byte(","))
			}
			write([]byte(strings.TrimSpace(fmt.Sprintf("%q:", k))))
			writeCanonical(write, t[k])
		}
		write([]byte("}"))
	case []any:
		write([]byte("["))
		for i, e := range t {
			if i > 0 {
				write([]byte(","))
			}
			writeCanonical(write, e)
		}
		write([]byte("]"))
	default:
		raw, _ := json.Marshal(t)
		write(raw)
	}
}
