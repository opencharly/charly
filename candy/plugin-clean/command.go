package clean

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// command.go — the externalized `charly clean` command. The plugin OWNS the flag grammar, the
// category orchestration, the output, AND (K1-alpha core-minimization) the SHARED retention
// ENGINE itself (retention.go — image-tag / build-candy / check-run pruning + the --deep
// store-wide dangling-image purge, also called by `charly box build` / `charly check run` /
// `charly box list tags` via verb:retention, the peer-adapter pattern verb:credential/verb:gpu/
// verb:tunnel already use). The engine runs LOCALLY here — no wire hop for the plugin's own CLI.
// The ONE thing this plugin genuinely cannot compute is the project's defaults.keep_images /
// keep_check_runs (needs the core LoadConfig loader, K-wave migration inventory), fetched via the
// small "retention-defaults" HostBuild seam. The pkg/arch makepkg sweep is a single-caller pure
// file op done locally too. No hidden core-command forward.
//
// clean is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process
// and gets the in-proc reverse channel (provider_command_external.go dispatchInProcCommand
// threads it), so HostBuild("retention-defaults") reaches the one host-coupled piece it still
// needs. The out-of-process cliMain path has NO reverse channel, so --invalidate/images/check/deep
// (which need the resolved keep-defaults) error there; runCleanCLI's own engine calls otherwise
// work standalone (list/invalidate need no defaults).

// runCleanCLI parses the clean flags and drives the categories: --invalidate (targeted image-tag
// invalidation), images (+ build-candy staging), check runs, the store-wide --deep purge, and the
// local makepkg sweep.
func runCleanCLI(ctx context.Context, exec *sdk.Executor, args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "Print everything that would be removed; touch nothing")
	images := fs.Bool("images", false, "Only image-tag retention")
	check := fs.Bool("check", false, "Only check-run retention")
	deep := fs.Bool("deep", false, "Purge every untagged/dangling image in local storage (not just charly-labeled) plus any layer blobs they alone held — reports UP TO the summed image size, since layers SHARED with kept images reduce actual reclaim; pair with --invalidate for the fullest reclaim. Runs ONLY this category unless combined with --images/--check")
	keep := fs.Int("keep", 0, "Override the retention count for this run (0 = use defaults:)")
	invalidate := fs.String("invalidate", "", "Remove every charly-labeled image tag matching this glob (full ref or last path segment); runs ONLY the invalidation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	tag := "removed"
	if *dryRun {
		tag = "would remove"
	}

	// --invalidate: targeted image-tag invalidation ONLY. Needs no keep-defaults.
	if *invalidate != "" {
		reply := runRetention(spec.RetentionRequest{Dir: dir, DryRun: *dryRun, Invalidate: *invalidate})
		if reply.Error != "" {
			return fmt.Errorf("%s", reply.Error)
		}
		fmt.Printf("invalidate: %s %d tag(s) matching %q\n", tag, len(reply.ImageRefs), *invalidate)
		for _, r := range reply.ImageRefs {
			fmt.Printf("  %s\n", r)
		}
		return nil
	}

	doImages, doCheck, doDeep, doMakepkg := cleanCategories(*images, *check, *deep)

	if doImages || doCheck || doDeep {
		keepImages, keepCheck, derr := fetchRetentionDefaults(ctx, exec, dir)
		if derr != nil {
			return derr
		}
		reply := runRetention(spec.RetentionRequest{
			Dir: dir, DryRun: *dryRun, Images: doImages, Check: doCheck, Deep: doDeep,
			Keep: *keep, KeepImages: keepImages, KeepCheckRuns: keepCheck,
		})
		if reply.Error != "" {
			return fmt.Errorf("%s", reply.Error)
		}
		if doImages {
			fmt.Printf("images: %s %d tag(s) (keep_images=%d)\n", tag, len(reply.ImageRefs), reply.KeepImages)
			for _, r := range reply.ImageRefs {
				fmt.Printf("  %s\n", r)
			}
			fmt.Printf("dangling: %s %d untagged charly image(s)\n", tag, len(reply.DanglingIDs))
			for _, id := range reply.DanglingIDs {
				fmt.Printf("  %s\n", id)
			}
			if len(reply.StagingDirs) > 0 {
				fmt.Printf("staging: %s %d dead buildah staging dir(s)\n", tag, len(reply.StagingDirs))
				for _, p := range reply.StagingDirs {
					fmt.Printf("  %s\n", p)
				}
			}
			fmt.Printf("build: %s %d staging dir(s) under .build/_candy (keep_images=%d)\n", tag, len(reply.BuildDirs), reply.KeepImages)
			for _, p := range reply.BuildDirs {
				fmt.Printf("  %s\n", p)
			}
		}
		if doCheck {
			fmt.Printf("check: %s %d run artifact(s) (keep_check_runs=%d, NOTES.md preserved)\n", tag, len(reply.CheckPaths), reply.KeepCheckRuns)
			for _, p := range reply.CheckPaths {
				fmt.Printf("  %s\n", p)
			}
		}
		if doDeep {
			fmt.Printf("deep: %s %d untagged image(s) store-wide (up to %s reclaimable — shared layers may reduce actual reclaim; pair with --invalidate for the fullest reclaim)\n", tag, len(reply.DeepIDs), kit.HumanBytes(reply.DeepBytes))
			for _, id := range reply.DeepIDs {
				fmt.Printf("  %s\n", id)
			}
		}
	}

	if doMakepkg {
		paths := cleanMakepkgArtifacts(dir, *dryRun)
		fmt.Printf("makepkg: %s %d leftover(s) under pkg/arch\n", tag, len(paths))
		for _, p := range paths {
			fmt.Printf("  %s\n", p)
		}
	}
	return nil
}

// cleanCategories resolves the --images/--check/--deep flags into which categories run this
// invocation. --images and --check keep their pre-existing "only this" semantics (any one given
// alone suppresses the other two default categories, including makepkg). --deep joins that same
// "explicit category" gate but NEVER fires implicitly: on a plain `charly clean` (no flags at
// all) doDeep is always false — the store-wide untagged-image sweep is a strictly broader
// operation than the default per-charly-labeled retention (it can remove far more, and scans the
// whole local store), so it stays opt-in-only and the default `charly clean` behavior is
// unchanged. Passing --deep alone runs ONLY the deep category (mirroring --invalidate's "runs
// ONLY this"); combine it with --images/--check to run more than one category in one invocation.
func cleanCategories(images, check, deep bool) (doImages, doCheck, doDeep, doMakepkg bool) {
	anyCategory := images || check || deep
	doImages = images || !anyCategory
	doCheck = check || !anyCategory
	doDeep = deep
	doMakepkg = !anyCategory
	return doImages, doCheck, doDeep, doMakepkg
}

// fetchRetentionDefaults asks the host to resolve defaults.keep_images/keep_check_runs via the
// small "retention-defaults" HostBuild seam — the ONE thing the retention engine (retention.go)
// genuinely cannot compute itself (it needs the core LoadConfig loader). exec is nil on the
// out-of-process cliMain path (no reverse channel) → a clear error naming the compiled-in
// requirement for the categories that need a resolved default.
func fetchRetentionDefaults(ctx context.Context, exec *sdk.Executor, dir string) (keepImages, keepCheck int, err error) {
	if exec == nil {
		return 0, 0, fmt.Errorf("charly clean --images/--check/--deep requires compiled-in placement (the retention-defaults host seam is unavailable out-of-process)")
	}
	reqJSON, err := json.Marshal(spec.RetentionRequest{Dir: dir})
	if err != nil {
		return 0, 0, err
	}
	resJSON, err := exec.HostBuild(ctx, "retention-defaults", reqJSON)
	if err != nil {
		return 0, 0, err
	}
	var reply spec.RetentionReply
	if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
		return 0, 0, uerr
	}
	if reply.Error != "" {
		return 0, 0, fmt.Errorf("%s", reply.Error)
	}
	return reply.KeepImages, reply.KeepCheckRuns, nil
}

// cleanMakepkgArtifacts removes the one-time makepkg build leftovers under pkg/arch (src/, pkg/,
// *.pkg.tar.zst, *.log) — pure transient waste (the package is already installed via pacman). Moved
// from charly core (its sole caller). Returns the paths removed.
func cleanMakepkgArtifacts(projectDir string, dryRun bool) []string {
	base := filepath.Join(projectDir, "pkg", "arch")
	var targets []string
	for _, sub := range []string{"src", "pkg"} {
		p := filepath.Join(base, sub)
		if _, err := os.Stat(p); err == nil {
			targets = append(targets, p)
		}
	}
	for _, pat := range []string{"*.pkg.tar.zst", "*.log"} {
		matches, _ := filepath.Glob(filepath.Join(base, pat))
		targets = append(targets, matches...)
	}
	var removed []string
	for _, p := range targets {
		if dryRun {
			removed = append(removed, p)
			continue
		}
		if err := os.RemoveAll(p); err == nil {
			removed = append(removed, p)
		}
	}
	return removed
}
