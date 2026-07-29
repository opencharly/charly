package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// host_prep.go — the plugin-side build-context FS PREP (K3 host-prep move, coneB-render). Formerly
// the host-fs half of charly's hostBuildPrep ("buildengine-prep" leg): cleanStaleBuildDirs /
// writeContextIgnore / createRemoteCandyCopies / ensureCharlyBinaryFresh are pure filesystem/exec
// operations over data (cfg/layers/resolved) resolveBuildEngine already computed plugin-side — no
// host-only dependency (a plugin process has the same OS-level filesystem/exec access the host
// process does). Proven by RCA: the host used to redo a FULL NewGenerator re-scan+re-resolve just
// to reach these four pure functions, and that second Generator's OWN render-prep output was never
// read by anything downstream. The ONE genuine host dependency — the bootstrap-embedded
// context_ignore_baseline (charly/charly.yml's //go:embed, unreachable from this separate module) —
// stays a thin data-only HostBuild("buildengine-context-ignore-baseline") leg.

// contextIgnoreFiles are the two engine-native build-context ignore files charly generates. podman
// reads .containerignore (preferring it) or .dockerignore; docker reads only .dockerignore. Emitting
// both from one source covers both engines with no divergent hand-maintained dotfile. (Byte-identical
// to the former charly/generate.go var.)
var contextIgnoreFiles = []string{".containerignore", ".dockerignore"}

// contextIgnoreBaselineLeg fetches the bootstrap-embedded context_ignore_baseline patterns.
func contextIgnoreBaselineLeg(ctx context.Context, ex *sdk.Executor) ([]string, error) {
	var out []string
	if err := hostBuildJSON(ctx, ex, "buildengine-context-ignore-baseline", struct{}{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// writeContextIgnore renders the build-context exclude list (baseline + defaults.context_ignore)
// into BOTH .containerignore and .dockerignore at the project root (the build context root). Single
// source of values, two render targets — keeps podman and docker builds in lockstep without a
// hand-maintained dotfile. Insertion order is deterministic (fixed baseline, then author-ordered
// config), duplicates collapsed. Byte-identical to the former charly/generate.go
// (*Generator).writeContextIgnore.
func writeContextIgnore(dir string, cfg *spec.Config, baseline []string) error {
	seen := make(map[string]bool)
	var patterns []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		patterns = append(patterns, p)
	}
	for _, p := range baseline {
		add(p)
	}
	if cfg != nil {
		for _, p := range cfg.Defaults.ContextIgnore {
			add(p)
		}
	}

	var b strings.Builder
	for _, name := range contextIgnoreFiles {
		b.Reset()
		fmt.Fprintf(&b, "# %s (generated -- do not edit; source: defaults.context_ignore in charly.yml)\n", name)
		for _, p := range patterns {
			b.WriteString(p)
			b.WriteByte('\n')
		}
		if err := kit.AtomicWriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return nil
}

// cleanStaleBuildDirs removes image directories in .build/ that don't correspond to any enabled
// image, and removes leftover files like docker-bake.hcl. Byte-identical to the former
// charly/generate.go (*Generator).cleanStaleBuildDirs.
func cleanStaleBuildDirs(buildDir string, boxes map[string]*buildkit.ResolvedBox) error {
	entries, err := os.ReadDir(buildDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			// Skip charly-managed staging dirs (_candy, _buildconfig, .locks, transient ._*.tmp.*
			// dirs): they are NOT images, and removing them races a concurrent build that is COPYing
			// from / locking on them.
			if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
				continue
			}
			if _, exists := boxes[name]; !exists {
				path := filepath.Join(buildDir, name)
				if err := os.RemoveAll(path); err != nil {
					return fmt.Errorf("removing stale dir %s: %w", path, err)
				}
				fmt.Fprintf(os.Stderr, "Removed stale build dir: .build/%s\n", name)
			}
		} else if entry.Name() == "docker-bake.hcl" {
			// Remove leftover HCL file from pre-charly-build era.
			path := filepath.Join(buildDir, entry.Name())
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("removing stale file %s: %w", path, err)
			}
			fmt.Fprintf(os.Stderr, "Removed stale file: .build/%s\n", entry.Name())
		}
	}
	return nil
}

// createRemoteCandyCopies copies remote candy directories into versioned
// .build/_candy/<name>.<version>/ dirs so that Docker/Podman can access them from the build context.
// Uses hard copies instead of symlinks because Podman doesn't follow symlinks that point outside the
// build context. Byte-identical to the former charly/generate.go (*Generator).createRemoteCandyCopies.
func createRemoteCandyCopies(buildDir string, candies map[string]spec.CandyReader) error {
	hasRemote := false
	for _, layer := range candies {
		if layer.GetRemote() {
			hasRemote = true
			break
		}
	}
	if !hasRemote {
		// No remote candies → no image COPYs from _candy, so any stale _candy is unreferenced and
		// harmless (pruned by `charly clean`). Leave it.
		return nil
	}

	// Each remote candy is staged into its OWN version-keyed dir .build/_candy/<name>.<version>/ —
	// built in a per-process temp then installed via renameat2(RENAME_EXCHANGE). Version-keying keeps
	// DISTINCT candy versions in DISTINCT dirs, so two concurrent builds resolving a candy at
	// different versions never clobber each other. The atomic install closes the within-version
	// concurrent-COPY race; identical content → identical bytes → podman's cache still hits.
	candyRoot := filepath.Join(buildDir, "_candy")
	if err := os.MkdirAll(candyRoot, 0o755); err != nil {
		return err
	}
	for ref, layer := range candies {
		if !layer.GetRemote() {
			continue
		}
		tmp, err := os.MkdirTemp(candyRoot, "."+layer.GetName()+".tmp.*")
		if err != nil {
			return err
		}
		// Copy the candy's CONTENTS (trailing /.) into the temp so the versioned dir holds the files
		// directly (the Containerfile COPYs `<dir>/ /`).
		cmd := exec.Command("cp", "-a", layer.GetSourceDir()+"/.", tmp)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("copying remote candy %s: %s: %w", ref, string(out), err)
		}
		if err := kit.InstallDirAtomic(tmp, filepath.Join(candyRoot, deploykit.CandyStageDirName(layer))); err != nil {
			return fmt.Errorf("installing remote candy %s: %w", ref, err)
		}
	}

	return nil
}

// ensureCharlyBinaryFresh rebuilds candy/charly/bin/charly when any image whose resolved candy
// chain includes the `charly` candy is in scope for the current build. Without this, podman build
// would COPY whatever stale binary happens to live at candy/charly/bin/charly — silently baking
// obsolete CLI behaviour into the image. Skipped (with a one-line warning) when `go` is not on PATH,
// so an end-user with a packaged charly install does not see a hard error. Byte-identical to the
// former charly/host_build_buildengine.go.
func ensureCharlyBinaryFresh(dir string, boxes map[string]*buildkit.ResolvedBox, requested []string) error {
	in := requested
	if len(in) == 0 {
		in = make([]string, 0, len(boxes))
		for name := range boxes {
			in = append(in, name)
		}
	}
	needs := false
	for _, name := range in {
		img, ok := boxes[name]
		if !ok {
			continue
		}
		if slices.Contains(img.Candy, "charly") {
			needs = true
		}
		if needs {
			break
		}
	}
	if !needs {
		return nil
	}

	binPath := filepath.Join(dir, kit.DefaultCandyDir, "charly", "bin", "charly")
	srcDir := filepath.Join(dir, "charly")

	// Downstream workspaces (project trees that `import:` upstream opencharly via `@github.com/...`)
	// don't ship the charly Go source. Without ./charly to rebuild from, there's nothing to refresh —
	// the embedded candy chain will use the cached upstream binary at
	// <upstream-cache>/candy/charly/bin/charly which is already up-to-date relative to upstream's
	// charly source.
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return nil
	}

	upToDate, err := buildkit.CharlyBinaryUpToDate(binPath, srcDir)
	if err == nil && upToDate {
		return nil
	}

	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintf(os.Stderr, "charly: warning: `go` not on PATH; skipping candy/charly/bin/charly rebuild (image will use existing binary)\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "charly: rebuilding candy/charly/bin/charly from ./charly before image build\n")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runHostFSPrep runs the FS-prep quartet in the SAME order the former host hostBuildPrep did
// (clean → mkdir → context-ignore → remote-candy-copies → ensureCharlyBinaryFresh), replacing the
// prepLeg HostBuild round-trip's FS-prep half.
func runHostFSPrep(ctx context.Context, ex *sdk.Executor, dir, buildDir string, cfg *spec.Config, layers map[string]spec.CandyReader, resolved map[string]*buildkit.ResolvedBox, boxes []string, generateOnly bool) error {
	if err := cleanStaleBuildDirs(buildDir, resolved); err != nil {
		return fmt.Errorf("cleaning stale build dirs: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("creating .build directory: %w", err)
	}
	baseline, err := contextIgnoreBaselineLeg(ctx, ex)
	if err != nil {
		return fmt.Errorf("fetching context-ignore baseline: %w", err)
	}
	if err := writeContextIgnore(dir, cfg, baseline); err != nil {
		return fmt.Errorf("writing context ignore files: %w", err)
	}
	if err := createRemoteCandyCopies(buildDir, layers); err != nil {
		return fmt.Errorf("creating remote candy symlinks: %w", err)
	}
	if !generateOnly {
		if err := ensureCharlyBinaryFresh(dir, resolved, boxes); err != nil {
			return fmt.Errorf("refreshing charly binary: %w", err)
		}
	}
	return nil
}
