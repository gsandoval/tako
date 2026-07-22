package local

import (
	"fmt"
	"os"
	"strings"
)

// LoadEnvFile reads the local secret source: `name=value` lines, `#`
// comments, blank lines. No quoting, no interpolation — a secret value
// is everything after the first '='. A missing file is not an error (an
// app with no secrets needs no .env).
func LoadEnvFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%s:%d: not a name=value line", path, i+1)
		}
		values[strings.TrimSpace(name)] = value
	}
	return values, nil
}
