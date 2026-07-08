package main

// ensure_image.go — THE CANONICAL ENSURE-IMAGE PATH for charly.
//
// `EnsureImagePresent` is the single source of truth used by every
// command that needs a container image present in local podman
// storage: deploys (BuilderRun), the check preflight, the operator-
// facing `charly box pull` verb, and the engine-transfer path
// (`EnsureImage` in transfer.go). One contract, one implementation,
// one set of failure modes — no per-caller divergence (R3).
//
// Three input forms accepted, mirroring `charly box pull`:
//
//   - Short name (e.g. "check-target") — resolved via `cfg.Box`
//     to a registry ref, then pulled. Build-fallback uses the same
//     short name as the input to `charly box build`.
//
//   - Fully-qualified registry ref (e.g.
//     "ghcr.io/opencharly/check-target:2026.124.1253") — pulled as-is.
//     Build-fallback reverse-resolves the basename against
//     `cfg.Box`; when the basename matches a project image entry,
//     the local build runs that entry. This is what makes the
//     operator's `ghcr.io/opencharly/arch-builder:<tag>`
//     buildable on a CachyOS host that has no ghcr.io credentials.
//
//   - Remote project ref (e.g.
//     "@github.com/opencharly/charly/check-target:latest") —
//     resolved via `ResolveRemoteImage` (operator-side repo download)
//     to a registry ref, then pulled. No build fallback for remote
//     refs (the remote repo's charly.yml resolution already gave us
//     the canonical ref).
//
// Algorithm:
//   1. LocalImageExists short-circuit — already present, no-op.
//   2. `podman pull <ref>` — preferred path.
//   3. `charly box build <name>` — fallback when (a) the ref maps to a
//      project image (short-name lookup or basename reverse-resolve),
//      AND (b) the pull failed for any reason. Build is local; SSH
//      executors are not in scope for this surface.
//
// Stateless — no install ledger, no ReverseOp, no `--reclaim-images`
// flag. Operators who want podman storage reclaimed run
// `podman image prune` or `podman rmi` themselves.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// EnsureImagePresent is the canonical helper. Every command that
// needs a container image MUST go through this entry point — never
// shell out to `podman pull` or rely on `podman run`'s implicit
// auto-pull.
func EnsureImagePresent(ctx context.Context, image string, cfg *Config, projectDir string) error {
	if image == "" {
		return fmt.Errorf("EnsureImagePresent: empty image identifier")
	}

	// Short-circuit if the image is already present in local storage.
	// For short names we resolve to a registry ref first; for full
	// refs the input is already the storage key.
	if ref, _ := resolveImageRefForEnsure(image, cfg, projectDir); ref != "" {
		if LocalImageExists("podman", ref) {
			fmt.Fprintf(os.Stderr, "ensure-image: %s present\n", ref)
			return nil
		}
	}

	// Try pull. Resolves remote (@github.com/...) refs via
	// ResolveRemoteImage; full refs pass through; short names resolve
	// to a registry ref via cfg.
	pullRef, perr := pullRefForEnsure(image, cfg, projectDir)
	if perr == nil && pullRef != "" {
		fmt.Fprintf(os.Stderr, "ensure-image: pulling %s\n", pullRef)
		if err := podmanPullForEnsure(ctx, pullRef); err == nil {
			return nil
		} else {
			fmt.Fprintf(os.Stderr, "ensure-image: pull %s failed: %v\n", pullRef, err)
		}
	} else if perr != nil {
		fmt.Fprintf(os.Stderr, "ensure-image: resolve %s: %v\n", image, perr)
	}

	// Fallback: remote ref → build from the cached @github.com/... repo
	// using the same workflow as `charly box build @<ref>`.
	stripped := StripURLScheme(image)
	if IsRemoteImageRef(stripped) {
		if rctx, err := ResolveRemoteImage(stripped, ""); err == nil {
			fmt.Fprintf(os.Stderr, "ensure-image: building remote %s from cached source\n", image)
			rt, rerr := ResolveRuntime()
			if rerr == nil {
				if berr := rctx.BuildImage(rt, ""); berr == nil {
					return nil
				} else {
					return fmt.Errorf("ensure-image %q: pull failed and remote build failed: %w", image, berr)
				}
			}
		}
	}

	// Fallback: short-name local build. Applies when the identifier
	// maps to a short-name entry in `cfg.Box` — directly (it IS a
	// short name) or via basename reverse-resolution (it's a full ref
	// whose basename matches an entry).
	short := buildableShortName(image, cfg)
	if short != "" {
		fmt.Fprintf(os.Stderr, "ensure-image: building %s locally\n", short)
		bcmd := &BuildCmd{Boxes: []string{short}, Jobs: 4}
		if berr := bcmd.Run(); berr != nil {
			return fmt.Errorf("ensure-image %q: pull failed and local build failed: %w", image, berr)
		}
		// The build produced the project's current-calver-tagged ref;
		// when the input ref pinned a specific tag (e.g. an older
		// builder version on a kind:local install_opt), alias the
		// just-built image to that tag so callers using
		// `--pull=never` find the requested ref locally. Skipped when
		// the input was already a short name (no pinned tag).
		if cfg != nil {
			if resolved, err := cfg.ResolveBox(short, "", projectDir, ResolveOpts{}); err == nil {
				produced := resolveShellImageRef(resolved.Registry, resolved.Name, "")
				if produced != "" && produced != image && looksLikeFullRef(image) {
					if terr := podmanTagAlias(ctx, produced, image); terr != nil {
						fmt.Fprintf(os.Stderr, "ensure-image: warning: tag alias %s -> %s failed: %v\n", produced, image, terr)
					}
				}
			}
		}
		return nil
	}

	return fmt.Errorf("ensure-image %q: not present locally, pull failed, and no buildable short-name match in charly.yml — make the registry public, log in to the registry, or pre-build the image manually", image)
}

// podmanTagAlias adds a second tag to an existing local image. Used to
// satisfy a tag-pinned input ref after a local build produced a
// different (calver) tag of the same image.
func podmanTagAlias(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "podman", "tag", src, dst)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// resolveImageRefForEnsure converts a user-authored image identifier
// into a fully-qualified registry ref usable for `LocalImageExists`.
// Short names need cfg; full refs and remote refs pass through (the
// caller routes remote refs via pullRefForEnsure at pull time).
func resolveImageRefForEnsure(image string, cfg *Config, projectDir string) (string, error) {
	if image == "" {
		return "", fmt.Errorf("empty image")
	}
	stripped := StripURLScheme(image)
	if IsRemoteImageRef(stripped) {
		return image, nil
	}
	if looksLikeFullRef(image) {
		return image, nil
	}
	if cfg == nil {
		return "", fmt.Errorf("short name %q requires a project directory with charly.yml", image)
	}
	resolved, err := cfg.ResolveBox(image, "", projectDir, ResolveOpts{})
	if err != nil {
		return "", fmt.Errorf("resolving %q via charly.yml: %w", image, err)
	}
	return resolveShellImageRef(resolved.Registry, resolved.Name, ""), nil
}

// pullRefForEnsure returns the registry ref to hand to `podman pull`.
// Same as resolveImageRefForEnsure EXCEPT remote
// (@github.com/...) refs are walked through ResolveRemoteImage,
// which performs the operator-side repo download and returns the
// canonical registry ref declared in the remote project's charly.yml.
func pullRefForEnsure(image string, cfg *Config, projectDir string) (string, error) {
	stripped := StripURLScheme(image)
	if IsRemoteImageRef(stripped) {
		rctx, err := ResolveRemoteImage(stripped, "")
		if err != nil {
			return "", fmt.Errorf("resolving remote ref %q: %w", image, err)
		}
		return rctx.ImageRef, nil
	}
	return resolveImageRefForEnsure(image, cfg, projectDir)
}

// podmanPullForEnsure invokes `podman pull <ref>` on the local
// machine. Errors propagate verbatim; the caller decides whether to
// fall back to a local build.
func podmanPullForEnsure(ctx context.Context, ref string) error {
	cmd := exec.CommandContext(ctx, "podman", "pull", ref)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// buildableShortName returns the short name (project charly.yml key)
// that this identifier maps to, or "" when no fallback is possible.
//
// Algorithm:
//   - Short names (no slash, no @prefix) are returned as-is when
//     `cfg.Box[name]` exists.
//   - Full registry refs have their basename (last path segment,
//     before the tag) extracted and resolved via findBoxByLeaf,
//     which searches the root image map AND every imported namespace.
//     This is what lets `ghcr.io/opencharly/arch-builder:<tag>` fall
//     back to building the project's `arch-builder` image whether it
//     lives at the root or under an imported namespace (e.g. the
//     cachyos profile's `charly.arch-builder`).
//   - Remote `@github.com/...` refs are skipped — the remote
//     project's charly.yml already determined the canonical ref;
//     local build-fallback is not applicable.
func buildableShortName(image string, cfg *Config) string {
	if cfg == nil || cfg.Box == nil || image == "" {
		return ""
	}
	stripped := StripURLScheme(image)
	if IsRemoteImageRef(stripped) {
		return ""
	}
	// Strip tag if present. Be careful: a registry like
	// "localhost:5000/foo" has a colon BEFORE the first slash that's
	// the port, not the tag separator.
	work := image
	firstSlash := strings.Index(work, "/")
	lastColon := strings.LastIndex(work, ":")
	if lastColon >= 0 && (firstSlash < 0 || lastColon > firstSlash) {
		work = work[:lastColon]
	}
	// Take the last path segment.
	if i := strings.LastIndex(work, "/"); i >= 0 {
		work = work[i+1:]
	}
	if work == "" {
		return ""
	}
	// Route through the single namespace-aware resolver. The basename may map
	// to a root image (returned bare) OR to an image living in an imported
	// namespace (returned qualified, e.g. `charly.arch-builder`). Both forms are
	// buildable: ResolveBox and the `charly box build` target path are both
	// namespace-aware, so the build-fallback can build a namespaced builder
	// the same way it builds a root one.
	// A QUALIFIED (namespaced) input resolves directly — `fedora.fedora-builder`
	// names a buildable image as-is (the build path is namespace-aware); the
	// leaf lookup below can never match a dotted ref (leaves are bare names).
	if strings.Contains(work, ".") {
		if _, _, ok := cfg.resolveBoxRef(work); ok {
			return work
		}
	}
	if q, ok := cfg.findBoxByLeaf(work); ok {
		return q
	}
	return ""
}
