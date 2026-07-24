// Package build turns generated services into runnable container images
// the ko way (spec §4.5): CGO_ENABLED=0 static binary → appended as one
// layer on distroless → pushed by digest. No Docker daemon anywhere.
// Images are deterministic — fixed timestamps, trimmed paths — so the
// same source always produces the same digest, which is what makes the
// reconciler's did-anything-change check (C2) honest.
package build

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// DefaultBase is the runtime image every service rides on: static
// distroless, running as nonroot. The tag is resolved to a digest at
// build time — the produced image pins the digest, never the tag.
const DefaultBase = "gcr.io/distroless/static-debian12:nonroot"

// epoch is the fixed timestamp stamped on layers and config —
// determinism beats honest-but-useless build times (ko does the same).
var epoch = time.Unix(0, 0).UTC()

// Options configure one build run.
type Options struct {
	// Dir is the app's module directory (generated mains under .tako/gen).
	Dir string
	// App is the module's app name (labels the images).
	App string
	// Base overrides DefaultBase (tests point it at a local registry).
	Base string
	// Repo, when non-empty, pushes each image to <Repo>/<service>.
	Repo string
}

// Result describes one built (and possibly pushed) service image.
type Result struct {
	Service string
	// Digest is the image digest ("sha256:…").
	Digest string
	// Ref is the pushed, digest-pinned reference, or the OCI layout path
	// when the build was local-only.
	Ref string
	// Base is the digest-pinned base reference that was resolved.
	Base string
	// BinarySize is the compiled static binary's size in bytes.
	BinarySize int64
	// Pushed reports whether Ref is a registry reference.
	Pushed bool
}

// keychain resolves registry credentials the ambient way: gcloud/ADC
// for Google registries, then anything docker login left behind.
var keychain = authn.NewMultiKeychain(google.Keychain, authn.DefaultKeychain)

// Service builds the image for one generated service and, when
// opts.Repo is set, pushes it.
func Service(opts Options, service string) (Result, error) {
	bin, size, err := compile(opts.Dir, service)
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(bin)

	base, baseRef, err := resolveBase(opts.Base)
	if err != nil {
		return Result{}, err
	}
	img, err := assemble(base, bin, service, opts.App)
	if err != nil {
		return Result{}, err
	}
	digest, err := img.Digest()
	if err != nil {
		return Result{}, err
	}

	res := Result{Service: service, Digest: digest.String(), Base: baseRef, BinarySize: size}
	if opts.Repo == "" {
		path := filepath.Join(opts.Dir, ".tako", "images", service)
		if err := writeLayout(path, img); err != nil {
			return Result{}, err
		}
		res.Ref = path
		return res, nil
	}
	ref, err := push(img, opts.Repo, service, digest)
	if err != nil {
		return Result{}, err
	}
	res.Ref = ref
	res.Pushed = true
	return res, nil
}

// compile produces the service's static linux/amd64 binary.
func compile(dir, service string) (path string, size int64, err error) {
	out := filepath.Join(dir, ".tako", "bin", service+"-linux-amd64")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -buildid=", "-o", out,
		"./"+filepath.Join(".tako", "gen", service))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if msg, err := cmd.CombinedOutput(); err != nil {
		return "", 0, fmt.Errorf("build %s: %s", service, strings.TrimSpace(string(msg)))
	}
	info, err := os.Stat(out)
	if err != nil {
		return "", 0, err
	}
	return out, info.Size(), nil
}

// resolveBase fetches the base image and pins its digest.
func resolveBase(base string) (v1.Image, string, error) {
	if base == "" {
		base = DefaultBase
	}
	ref, err := name.ParseReference(base)
	if err != nil {
		return nil, "", fmt.Errorf("base image %q: %w", base, err)
	}
	img, err := remote.Image(ref, remote.WithAuthFromKeychain(keychain))
	if err != nil {
		return nil, "", fmt.Errorf("resolve base %s: %w", base, err)
	}
	digest, err := img.Digest()
	if err != nil {
		return nil, "", err
	}
	pinned := ref.Context().Name() + "@" + digest.String()
	return img, pinned, nil
}

// assemble appends the binary as the image's one extra layer and shapes
// the config: entrypoint /app/<service>, deterministic timestamps,
// tako labels (the reconciler's vocabulary starts here).
func assemble(base v1.Image, bin, service, app string) (v1.Image, error) {
	layer, err := binaryLayer(bin, service)
	if err != nil {
		return nil, err
	}
	img, err := mutate.AppendLayers(base, layer)
	if err != nil {
		return nil, err
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()
	cfg.Config.Entrypoint = []string{"/app/" + service}
	cfg.Config.Cmd = nil
	if cfg.Config.Labels == nil {
		cfg.Config.Labels = map[string]string{}
	}
	cfg.Config.Labels["tako.app"] = app
	cfg.Config.Labels["tako.service"] = service
	cfg.Created = v1.Time{Time: epoch}
	for i := range cfg.History {
		cfg.History[i].Created = v1.Time{Time: epoch}
	}
	return mutate.ConfigFile(img, cfg)
}

// binaryLayer tars the binary at /app/<service> with a fixed timestamp.
func binaryLayer(bin, service string) (v1.Layer, error) {
	raw, err := os.ReadFile(bin)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, hdr := range []tar.Header{
		{Name: "app/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: epoch},
		{Name: "app/" + service, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(raw)), ModTime: epoch},
	} {
		if err := tw.WriteHeader(&hdr); err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write(raw); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	blob := buf.Bytes()
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(blob)), nil
	})
}

// writeLayout writes the image as an OCI layout (crane/skopeo readable).
func writeLayout(path string, img v1.Image) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	idx := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: img})
	_, err := layout.Write(path, idx)
	return err
}

// push uploads the image and returns its digest-pinned reference.
func push(img v1.Image, repo, service string, digest v1.Hash) (string, error) {
	dst, err := name.ParseReference(repo + "/" + service)
	if err != nil {
		return "", fmt.Errorf("push repo %q: %w", repo, err)
	}
	if err := remote.Write(dst, img, remote.WithAuthFromKeychain(keychain)); err != nil {
		return "", classifyPushError(err, repo, service)
	}
	return dst.Context().Name() + "@" + digest.String(), nil
}

// classifyPushError separates the three registry failures a user actually
// hits — not authenticated, authenticated but not authorized, and repo
// missing — and prints the one command that fixes each (the permission
// model's friction rule: name the missing grant, never guess broader).
func classifyPushError(err error, repo, service string) error {
	var terr *transport.Error
	if !errors.As(err, &terr) {
		return fmt.Errorf("push %s: %w", service, err)
	}
	loc, project, repoName, arOK := parseARRepo(repo)
	switch terr.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("cannot authenticate to %s — no Google credentials; run \"gcloud auth login\" (or set GOOGLE_APPLICATION_CREDENTIALS) and retry", repo)
	case http.StatusForbidden:
		if arOK {
			return fmt.Errorf("push to %s denied — you are authenticated but lack write access; grant it once:\n  gcloud artifacts repositories add-iam-policy-binding %s --project %s --location %s --member user:$(gcloud config get account) --role roles/artifactregistry.writer\n(if the Artifact Registry API is disabled, enable it first: gcloud services enable artifactregistry.googleapis.com --project %s)", repo, repoName, project, loc, project)
		}
		return fmt.Errorf("push to %s denied — you are authenticated but lack write access to this repository", repo)
	case http.StatusNotFound:
		if arOK {
			return fmt.Errorf("repository %s does not exist — create it once:\n  gcloud artifacts repositories create %s --project %s --location %s --repository-format docker", repo, repoName, project, loc)
		}
		return fmt.Errorf("repository %s does not exist", repo)
	}
	return fmt.Errorf("push %s: %w", service, err)
}

// parseARRepo splits an Artifact Registry docker repo path
// (<loc>-docker.pkg.dev/<project>/<repo>) so error remediations can be
// copy-pasteable, real commands.
func parseARRepo(repo string) (loc, project, name string, ok bool) {
	parts := strings.Split(repo, "/")
	if len(parts) < 3 || !strings.HasSuffix(parts[0], "-docker.pkg.dev") {
		return "", "", "", false
	}
	return strings.TrimSuffix(parts[0], "-docker.pkg.dev"), parts[1], parts[2], true
}
