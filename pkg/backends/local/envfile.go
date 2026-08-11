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

// SetEnvValue writes name=value into the .env at path, and returns
// whether the name was already there.
//
// It rewrites the file line by line rather than re-serialising a map,
// because a developer's .env is a file they own: comments explaining
// where a key came from, blank lines grouping related values, and the
// order they put things in all survive. A secret command that
// reformatted the file every time would be one people stop using.
func SetEnvValue(path, name, value string) (replaced bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if key, _, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(key) == name {
				out = append(out, name+"="+value)
				replaced = true
				continue
			}
		}
		out = append(out, line)
	}
	if !replaced {
		out = append(out, name+"="+value)
	}
	// A file that did not exist starts with our line, not a blank one.
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	return replaced, os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o600)
}

// RemoveEnvValue drops name from the .env at path, keeping everything
// else exactly as it was. Removing something absent is not an error.
func RemoveEnvValue(path, name string) (removed bool, err error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if key, _, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(key) == name {
				removed = true
				continue
			}
		}
		out = append(out, line)
	}
	if !removed {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o600)
}
