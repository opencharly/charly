package build

// ensure.go — the candy-side DRIVE behind the build:ensure word (core-min wave 3, build-engine
// cluster relocation): ensure a container image is present in local podman storage, falling back
// to a local (or remote-cached) build when the identifier maps to a project charly.yml entry.
//
// This is the SAME contract the former core ensure-image helper used to offer (that file is
// DELETED) plus the former core cross-engine-transfer tier — every former Go-level caller now
// reaches it through dispatchBuildEnsure (charly/dispatch_build_ensure.go), which Invokes this
// word.
//
// Three tiers, each independent, mirroring the deleted core transfer + ensure-image helpers
// exactly:
//
//  1. Already-present short-circuit (kit.LocalImageExists in the run engine).
//  2. Cross-engine transfer (`docker save | podman load`) when the build engine != the run
//     engine AND the image is present in the build engine's storage.
//  3. Resolve + pull + build-fallback: resolve the identifier against the project's charly.yml
//     via the "box-ref-resolve" HostBuild seam (a remote @github.com/... ref instead routes
//     through "remote-image-resolve"), attempt `podman pull`, and on failure fall back to a
//     local build — reached via the SAME in-process runBoxBuild this candy already owns for the
//     build:box word (no new build seam), tag-aliasing the produced image onto a pinned-tag
//     input ref when the two differ.
//
// Only tier 3 needs the host at all (project-coupled resolution); tiers 1-2 and the podman
// pull/tag exec are pure sdk/kit + os/exec, no HostBuild round trip.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// runBoxEnsure is the build:ensure Invoke body.
func runBoxEnsure(ctx context.Context, ex *sdk.Executor, req spec.BuildEnsureRequest) error {
	if req.Image == "" {
		return fmt.Errorf("ensure-image: empty image identifier")
	}
	runEngine := req.RunEngine
	if runEngine == "" {
		runEngine = "podman"
	}

	// Tier 1: already present. Only matches a FULL ref (podman/docker "image exists" needs a
	// real storage key — a bare short name never matches here); a short name's existence check
	// happens in tier 3 via the resolved ref from the "box-ref-resolve" seam. Prints the SAME
	// "present" line the former core ensure-image helper's top-level short-circuit did, so every
	// caller (charly box pull included) sees consistent progress output regardless of which tier
	// hit.
	if kit.LocalImageExists(runEngine, req.Image) {
		fmt.Fprintf(os.Stderr, "ensure-image: %s present\n", req.Image)
		return nil
	}

	// Tier 2: cross-engine transfer — faster than a network pull and works offline.
	if req.BuildEngine != "" && req.BuildEngine != runEngine && kit.LocalImageExists(req.BuildEngine, req.Image) {
		return kit.TransferImage(req.BuildEngine, runEngine, req.Image)
	}

	// Tier 3: resolve + pull + build-fallback.
	return ensureViaResolveAndBuild(ctx, ex, req.Image, req.Dir)
}

// ensureViaResolveAndBuild implements tier 3. A remote (@github.com/...) ref resolves via the
// "remote-image-resolve" seam (ResolveRemoteImage host-side); every other identifier resolves
// via "box-ref-resolve" (ResolveBox / FindBoxByLeaf host-side). Both branches mirror the deleted
// core ensure-image helper's algorithm verbatim: check existence of the resolved ref, attempt a
// pull, and on failure fall back to a build.
func ensureViaResolveAndBuild(ctx context.Context, ex *sdk.Executor, image, dir string) error {
	stripped := kit.StripURLScheme(image)
	if spec.IsRemoteImageRef(stripped) {
		return ensureRemoteRef(ctx, ex, image, stripped)
	}
	return ensureProjectRef(ctx, ex, image, dir)
}

func ensureRemoteRef(ctx context.Context, ex *sdk.Executor, image, stripped string) error {
	rr, rerr := remoteImageResolve(ctx, ex, stripped, "")
	if rerr == nil && rr.Error == "" && rr.ImageRef != "" {
		if kit.LocalImageExists("podman", rr.ImageRef) {
			fmt.Fprintf(os.Stderr, "ensure-image: %s present\n", rr.ImageRef)
			return nil
		}
		fmt.Fprintf(os.Stderr, "ensure-image: pulling %s\n", rr.ImageRef)
		if err := podmanPull(ctx, rr.ImageRef); err == nil {
			return nil
		} else {
			fmt.Fprintf(os.Stderr, "ensure-image: pull %s failed: %v\n", rr.ImageRef, err)
		}
		fmt.Fprintf(os.Stderr, "ensure-image: building remote %s from cached source\n", image)
		if _, berr := runBoxBuild(ctx, ex, spec.BuildRequest{Boxes: []string{rr.BoxName}, Dir: rr.CacheDir}); berr != nil {
			return fmt.Errorf("ensure-image %q: pull failed and remote build failed: %w", image, berr)
		}
		return nil
	}
	errMsg := rr.Error
	if rerr != nil {
		errMsg = rerr.Error()
	}
	fmt.Fprintf(os.Stderr, "ensure-image: resolve %s: %s\n", image, errMsg)
	return fmt.Errorf("ensure-image %q: pull failed and remote build failed: %s", image, errMsg)
}

func ensureProjectRef(ctx context.Context, ex *sdk.Executor, image, dir string) error {
	br, err := boxRefResolve(ctx, ex, image, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ensure-image: resolve %s: %v\n", image, err)
		br = spec.BoxRefResolveReply{}
	}

	if br.ExistsRef != "" && kit.LocalImageExists("podman", br.ExistsRef) {
		fmt.Fprintf(os.Stderr, "ensure-image: %s present\n", br.ExistsRef)
		return nil
	}

	if br.PullRef != "" {
		fmt.Fprintf(os.Stderr, "ensure-image: pulling %s\n", br.PullRef)
		if perr := podmanPull(ctx, br.PullRef); perr == nil {
			return nil
		} else {
			fmt.Fprintf(os.Stderr, "ensure-image: pull %s failed: %v\n", br.PullRef, perr)
		}
	}

	if br.BuildFallbackShort == "" {
		return fmt.Errorf("ensure-image %q: not present locally, pull failed, and no buildable short-name match in charly.yml — make the registry public, log in to the registry, or pre-build the image manually", image)
	}

	fmt.Fprintf(os.Stderr, "ensure-image: building %s locally\n", br.BuildFallbackShort)
	if _, berr := runBoxBuild(ctx, ex, spec.BuildRequest{
		Boxes:           []string{br.BuildFallbackShort},
		Dir:             dir,
		IncludeDisabled: true,
		Jobs:            4,
	}); berr != nil {
		return fmt.Errorf("ensure-image %q: pull failed and local build failed: %w", image, berr)
	}

	// The build produced the project's current-CalVer-tagged ref; when the input ref pinned a
	// specific tag (e.g. an older builder version on a kind:local install_opts.builder_image),
	// alias the just-built image to that tag so callers using `--pull=never` find the requested
	// ref locally. Skipped when the input was already a short name (no pinned tag).
	if br.ProducedRef != "" && br.ProducedRef != image && kit.LooksLikeFullRef(image) {
		if terr := podmanTag(ctx, br.ProducedRef, image); terr != nil {
			fmt.Fprintf(os.Stderr, "ensure-image: warning: tag alias %s -> %s failed: %v\n", br.ProducedRef, image, terr)
		}
	}
	return nil
}

// boxRefResolve calls the "box-ref-resolve" HostBuild seam.
func boxRefResolve(ctx context.Context, ex *sdk.Executor, image, dir string) (spec.BoxRefResolveReply, error) {
	reqJSON, err := json.Marshal(spec.BoxRefResolveRequest{Image: image, Dir: dir})
	if err != nil {
		return spec.BoxRefResolveReply{}, err
	}
	replyJSON, err := ex.HostBuild(ctx, "box-ref-resolve", reqJSON)
	if err != nil {
		return spec.BoxRefResolveReply{}, err
	}
	var reply spec.BoxRefResolveReply
	if err := json.Unmarshal(replyJSON, &reply); err != nil {
		return spec.BoxRefResolveReply{}, fmt.Errorf("decode box-ref-resolve reply: %w", err)
	}
	return reply, nil
}

// remoteImageResolve calls the "remote-image-resolve" HostBuild seam.
func remoteImageResolve(ctx context.Context, ex *sdk.Executor, ref, tag string) (spec.RemoteImageResolveReply, error) {
	reqJSON, err := json.Marshal(spec.RemoteImageResolveRequest{Ref: ref, Tag: tag})
	if err != nil {
		return spec.RemoteImageResolveReply{}, err
	}
	replyJSON, err := ex.HostBuild(ctx, "remote-image-resolve", reqJSON)
	if err != nil {
		return spec.RemoteImageResolveReply{}, err
	}
	var reply spec.RemoteImageResolveReply
	if err := json.Unmarshal(replyJSON, &reply); err != nil {
		return spec.RemoteImageResolveReply{}, fmt.Errorf("decode remote-image-resolve reply: %w", err)
	}
	return reply, nil
}

// podmanPull invokes `podman pull <ref>` on the local machine. Errors propagate verbatim; the
// caller decides whether to fall back to a local build.
func podmanPull(ctx context.Context, ref string) error {
	cmd := exec.CommandContext(ctx, "podman", "pull", ref)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// podmanTag adds a second tag to an existing local image. Used to satisfy a tag-pinned input ref
// after a local build produced a different (CalVer) tag of the same image.
func podmanTag(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "podman", "tag", src, dst)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
