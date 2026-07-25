package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// Sites runs every declared WithBuild command — the frontend build IS
// part of tako deploy and tako dev (§2.11), executed on this machine
// with whatever toolchain is installed (the local-build philosophy).
// The output dir must exist and be non-empty afterward; say narrates
// one line per site. Sites without WithBuild need no step (the source
// dir serves as-is).
func Sites(dir string, g *analyzer.AppGraph, say func(format string, args ...any)) error {
	for _, r := range g.Resources {
		if r.Kind != "site" || r.Build == "" {
			continue
		}
		cmd := exec.Command("sh", "-c", r.Build)
		cmd.Dir = dir
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			tail := strings.TrimSpace(string(out))
			if len(tail) > 400 {
				tail = "…" + tail[len(tail)-400:]
			}
			return fmt.Errorf("site build failed: %s: %v — %s", r.Build, err, tail)
		}
		outDir := filepath.Join(dir, filepath.FromSlash(r.BuildOut))
		entries, err := os.ReadDir(outDir)
		if err != nil || len(entries) == 0 {
			return fmt.Errorf("site build %q succeeded but its output dir %s is missing or empty — WithBuild's second argument must name where the build writes", r.Build, r.BuildOut)
		}
		n := 0
		filepath.WalkDir(outDir, func(_ string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				n++
			}
			return nil
		})
		if say != nil {
			say("⛅ site      %s → %s (%d files)", r.Build, r.BuildOut, n)
		}
	}
	return nil
}
