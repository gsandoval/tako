package main

import "strings"

// normalizeArgs splits "--flag=value" tokens into "--flag", "value" so
// every subcommand parser accepts both "--flag value" and "--flag=value"
// uniformly. Normalization stops after a bare "--" argument separator.
func normalizeArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if strings.HasPrefix(a, "--") {
			if name, value, ok := strings.Cut(a, "="); ok && len(name) > 2 {
				out = append(out, name, value)
				continue
			}
		}
		out = append(out, a)
	}
	return out
}
