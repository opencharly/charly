package build

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// imageTags computes the tags for an image. charly is CalVer-only — it never emits `:latest`.
// Every built image carries exactly its CalVer tag (box.FullTag, e.g.
// `ghcr.io/opencharly/fedora:2026.114.1042`); short-name resolution goes through the
// `ai.opencharly.version` OCI label host-side.
func imageTags(box spec.BuildResolveBox) []string {
	return []string{box.FullTag}
}

// buildImage builds a single image with the configured engine. The rendered Containerfile is piped
// via stdin (-f -) to avoid race conditions with concurrent generate overwrites on disk. For
// Podman --push, the image is built locally (--manifest) without pushing; push happens separately
// after merge. The privileged builder-bootstrap (from: builder:<name>) already ran HOST-SIDE during
// build-prep, so this drive never runs it. The Containerfile content is rendered by plugin-build
// via deploykit.Generator (NOT shipped in the reply — #67 render-DRIVE move).
func (c driveConfig) buildImage(box spec.BuildResolveBox, containerfile string) error {
	tags := imageTags(box)

	// Per-image build lock: serialize concurrent builds of THIS image across charly processes
	// (so a shared intermediate built by many parallel beds is built COLD once — the others block
	// here, then cache-hit), while DISTINCT images (the leaf fan-out) take distinct locks and build
	// in parallel. Held across the podman build for this image only.
	release, err := kit.AcquireImageBuildLock(box.FullTag)
	if err != nil {
		return fmt.Errorf("acquiring build lock for %s: %w", box.Name, err)
	}
	defer func() { _ = release() }()

	var args []string
	if c.Push {
		args = c.buildPushArgs(tags, box.Platforms, box.Name, box.Registry)
	} else {
		args = c.buildLocalArgs(tags, box.Name, box.Registry)
	}

	fmt.Fprintf(os.Stderr, "\n--- Building %s ---\n", box.Name)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = c.Dir
	cmd.Stdin = strings.NewReader(containerfile)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build failed: %w", c.Engine, err)
	}

	return nil
}

// buildLocalArgs constructs args for a local (single-platform, load into store) build. Uses -f - to
// read the Containerfile from stdin. The host-resolved PodmanJobs feeds `--jobs` directly (no
// candy-side resolvePodmanJobs).
func (c driveConfig) buildLocalArgs(tags []string, name, registry string) []string {
	args := []string{c.Engine, "build", "--layers=true", "-f", "-"}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	if c.Platform != "" {
		args = append(args, "--platform", c.Platform)
	}
	if c.Engine == "podman" {
		args = append(args, "--jobs", strconv.Itoa(c.PodmanJobs))
	}
	args = append(args, c.cacheArgs(name, registry, c.Engine)...)
	args = append(args, ".")
	return args
}

// buildPushArgs constructs args for a multi-platform push build.
func (c driveConfig) buildPushArgs(tags []string, platforms []string, name, registry string) []string {
	if c.EngineName == "podman" {
		return c.buildPodmanPushArgs(tags, platforms, name, registry)
	}
	return c.buildDockerPushArgs(tags, platforms, name, registry)
}

func (c driveConfig) buildDockerPushArgs(tags []string, platforms []string, name, registry string) []string {
	args := []string{"docker", "buildx", "build", "--push", "-f", "-"}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	if len(platforms) > 0 {
		args = append(args, "--platform", strings.Join(platforms, ","))
	}
	args = append(args, c.cacheArgs(name, registry, "docker")...)
	args = append(args, ".")
	return args
}

func (c driveConfig) buildPodmanPushArgs(tags []string, platforms []string, name, registry string) []string {
	// Podman uses --manifest for multi-platform builds.
	args := []string{"podman", "build", "--layers=true", "-f", "-"}
	if len(tags) > 0 {
		args = append(args, "--manifest", tags[0])
	}
	if len(platforms) > 0 {
		args = append(args, "--platform", strings.Join(platforms, ","))
	}
	args = append(args, "--jobs", strconv.Itoa(c.PodmanJobs))
	args = append(args, c.cacheArgs(name, registry, "podman")...)
	args = append(args, ".")
	return args
}

// cacheArgs returns cache flags for the given image name based on the --cache setting.
// Default: "image" (read-only from registry) for local builds, "registry" (read+write) for push
// builds. Podman uses plain image refs for --cache-from/--cache-to (no tags allowed for
// --cache-to). Docker buildx uses type=registry,ref=... syntax with a separate cache repo.
func (c driveConfig) cacheArgs(name, registry, engine string) []string {
	if c.NoCache || c.Cache == "none" {
		return nil
	}

	cacheType := c.Cache
	// Auto-detect: default to "image" for local, "registry" for push.
	if cacheType == "" && registry != "" {
		if c.Push {
			cacheType = "registry"
		} else {
			cacheType = "image"
		}
	}

	switch cacheType {
	case "registry":
		if registry == "" {
			return nil
		}
		ref := fmt.Sprintf("%s/%s", registry, name)
		if engine == "podman" {
			// Podman --cache-to takes a plain repo ref (no tag, no type= syntax).
			// Intermediate build layers are pushed to the same repo as the image.
			return []string{
				"--cache-from", ref,
				"--cache-to", ref,
			}
		}
		// Docker buildx uses a separate cache repo with type=registry syntax.
		cacheRef := fmt.Sprintf("%s/cache:%s", registry, name)
		return []string{
			"--cache-from", fmt.Sprintf("type=registry,ref=%s", cacheRef),
			"--cache-to", fmt.Sprintf("type=registry,ref=%s,mode=max,compression=zstd", cacheRef),
		}
	case "gha":
		return []string{
			"--cache-from", fmt.Sprintf("type=gha,scope=%s", name),
			"--cache-to", fmt.Sprintf("type=gha,mode=max,scope=%s", name),
		}
	case "image":
		if registry == "" {
			return nil
		}
		ref := fmt.Sprintf("%s/%s", registry, name)
		return []string{"--cache-from", ref}
	default:
		return nil
	}
}

// pushImage pushes a Podman image to the registry for each tag with retry. Detects whether the
// primary tag is a manifest list or a regular image and uses the appropriate push command.
func pushImage(dir string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	// Check if the primary tag is a manifest list.
	isManifest := false
	checkCmd := exec.Command("podman", "manifest", "inspect", tags[0])
	checkCmd.Stdout = io.Discard
	checkCmd.Stderr = io.Discard
	if checkCmd.Run() == nil {
		isManifest = true
	}

	for _, tag := range tags {
		fmt.Fprintf(os.Stderr, "Pushing %s\n", tag)
		pushTag := tag
		if err := retryCmd(5*time.Second, func() error {
			var pushCmd *exec.Cmd
			if isManifest {
				pushCmd = exec.Command("podman", "manifest", "push", "--all", tags[0], "docker://"+pushTag)
			} else {
				pushCmd = exec.Command("podman", "push", tags[0], "docker://"+pushTag)
			}
			pushCmd.Dir = dir
			pushCmd.Stdout = os.Stderr
			pushCmd.Stderr = os.Stderr
			return pushCmd.Run()
		}); err != nil {
			kind := "push"
			if isManifest {
				kind = "manifest push"
			}
			return fmt.Errorf("podman %s %s failed: %w", kind, tag, err)
		}
	}
	return nil
}

// retryCmd retries fn up to maxAttempts times with exponential backoff starting at baseDelay.
func retryCmd(baseDelay time.Duration, fn func() error) error {
	const maxAttempts = 3
	var err error
	for i := range maxAttempts {
		if i > 0 {
			delay := baseDelay * time.Duration(1<<(i-1))
			fmt.Fprintf(os.Stderr, "Retry %d/%d after %v...\n", i, maxAttempts-1, delay)
			time.Sleep(delay)
		}
		err = fn()
		if err == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "Attempt %d/%d failed: %v\n", i+1, maxAttempts, err)
	}
	return err
}
