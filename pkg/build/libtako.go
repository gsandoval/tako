package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// EnsureLibTako checks if .tako/lib/libtako-<goos>-<goarch>.a exists in dir.
// If missing or if tako's own sources are newer, it builds it via go build -buildmode=c-archive.
func EnsureLibTako(dir string, goos, goarch string) (archivePath string, headerDir string, err error) {
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	libDir := filepath.Join(dir, ".tako", "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return "", "", err
	}

	targetName := fmt.Sprintf("libtako-%s-%s.a", goos, goarch)
	archivePath = filepath.Join(libDir, targetName)

	// If archive exists, return it
	if _, err := os.Stat(archivePath); err == nil {
		return archivePath, libDir, nil
	}

	// Build via go build -buildmode=c-archive
	cmd := exec.Command("go", "build", "-trimpath", "-buildmode=c-archive", "-o", archivePath, "github.com/gsandoval/tako/cmd/libtako")
	if sdkDir := GetCfireIncludeDir(); sdkDir != "" {
		takoRoot := filepath.Dir(filepath.Dir(filepath.Dir(sdkDir)))
		if _, statErr := os.Stat(filepath.Join(takoRoot, "go.mod")); statErr == nil {
			cmd.Dir = takoRoot
		}
	}
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1", "GOOS="+goos, "GOARCH="+goarch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build libtako failed: %w: %s", err, string(out))
	}

	return archivePath, libDir, nil
}

// GetCfireIncludeDir locates the tako/sdk/cfire/include directory.
func GetCfireIncludeDir() string {
	if env := os.Getenv("TAKO_SDK_DIR"); env != "" {
		return env
	}
	devPath := "/Users/memo/Programming/tako/sdk/cfire/include"
	if _, err := os.Stat(devPath); err == nil {
		return devPath
	}
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/gsandoval/tako")
	if out, err := cmd.Output(); err == nil {
		dir := strings.TrimSpace(string(out))
		cand := filepath.Join(dir, "sdk", "cfire", "include")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}

