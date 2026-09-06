package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/spec/spec"
)

// check_cmd.go — the residual host-side check-project plumbing after the K1-unblock wave's "live"
// and "feature-live" arms moved to candy/plugin-check (live_gather.go's pluginCheckRunLive,
// feature_run_gather.go's pluginCheckRunFeatureLive — both wired via command.go's Mode
// short-circuit; "feature-box" was traced and never had a live caller through this seam — see
// feature_run_gather.go's header; "feature-box" is now the plugin-side pluginCheckRunFeatureBox,
// reached from candy/plugin-box's command:feature over InvokeProvider — cone-C #31). What remains
// is resolveCheckRunnerContext — the SOLE remaining production content, feeding
// the new "check-load-plugins" seam (host_build_check_load_plugins.go). It STAYS core because it
// calls loadProjectPlugins directly, the same core-private registry-mutating mechanism
// loadDeployPlugins drives (#55 W3 B3 — moving it would have the plugin calling into itself). The
// external `target: local` deploy's own --verify path RELOCATED to candy/plugin-fleet's
// verify_local.go (#55 W3 B3 remainder) — it no longer lives here.

// The `charly check` exit-code contract (2 = checks failed, 3 = prereq skip) lives in
// the sdk (exitcode.CheckFailExitCode / exitcode.CheckSkippedExitCode); the plugin/main signal it
// across the module boundary via *exitcode.ExitCodeError. The `charly check` CLI + its
// exit-code plumbing live in command:check (candy/plugin-check).

// resolveCheckRunnerContext computes the committed-APK anchoring + loads the OUT-OF-TREE plugin
// candies a live baked-plan runner needs, so `charly check live` and `charly check feature run`
// resolve adb/appium `apk:` checks IDENTICALLY (R3). They previously diverged — only check live
// populated CandyDirs, so a committed-APK check passed under check live yet failed to anchor
// ("0 candies scanned") under feature run. Any RunModeLive runner that executes a baked plan
// MUST fold its result into the RunnerConfig (CandyDirs + CandyScanErr).
func resolveCheckRunnerContext(box, dir string, cfg *spec.Config, extraWords ...string) checkRunnerContext {
	// Scan the RESOLVED candy set ONCE (local + @github-fetched): it carries each
	// candy's SourceDir (committed-APK anchoring) AND its `plugin:` block, so one
	// scan feeds BOTH consumers (R3). A box that vendors all its candies via @github
	// (every box/<distro>) has no project-local Candy map, so the plugin set MUST
	// come from this scan — never from LoadUnified.
	//
	// ExtraCandyRefs adds the BED's own `add_candy:` candies to the collection: the
	// image-closure walk never reaches them, so a bed that add_candy's a host-side
	// PLUGIN candy (e.g. plugin-spice for the `spice:` check verb authored INLINE in
	// the bed plan, with no candy in the image closure requiring it) would otherwise
	// leave the plugin unloaded and the `spice:` step failing as an unknown verb.
	addCandy, refWords := deployNodePluginContext(dir, box)
	// The caller-supplied extra words (the instrument pipeline verbs — a SEPARATE
	// plugin-reference surface from the plan steps, RCA 2026-09-06) union into the
	// reference scan: the plan-step scan never sees the instrument pipeline, so the
	// pipeline verbs' serving plugins would otherwise never connect and the evidence
	// phase's blind word dispatch fails with "no provider registered".
	refWords = append(refWords, extraWords...)
	// The VM plugin candy (verb:libvirt) is external (out-of-process) and in no box's image
	// closure, so a bed whose plan dispatches `libvirt:` (e.g. check-fedora-vm's libvirt-verb-
	// dispatches step) needs it pulled in by its canonical ref — the same host-side-plugin pattern
	// as a bed add_candy'ing plugin-spice for `spice:`. Harmless for non-VM beds: loadProjectPlugins
	// build-connects it only if the plan references libvirt; in a bed CHARLY_REPO_OVERRIDE resolves
	// the ref to the local superproject under development.
	addCandy = append(addCandy, vmPluginCandyRef())
	candyMap, scanErr := ScanAllCandyWithConfigOpts(dir, cfg, spec.ResolveOpts{ExtraCandyRefs: addCandy})
	if scanErr != nil {
		return checkRunnerContext{CandyScanErr: fmt.Errorf("scanning candy source dirs: %w", scanErr)}
	}
	// Connect + register the OUT-OF-TREE plugin candies a `check: plugin: <verb>` step
	// REFERENCES, out-of-process (built-in plugins are already compiled in). Perf-scoped
	// via collectReferencedPluginWords: the candy/box plans + candy external_builder +
	// the bed's OWN refWords (its substrate kind + the inline plugin verbs in its
	// flattened plan — the `spice:` step above) name every plugin the bed dispatches, so
	// an UNREFERENCED plugin candy in the scan (the rest of a box/<distro> plugin set) is
	// not host-built while a referenced one always loads (over-load safe, never under). A
	// build/connect failure is surfaced as a warning; the bed's plugin check then fails
	// loudly via runPluginVerb's unresolved-verb path. The shared check-runner setup is
	// the ONE place every check path (box/live) loads plugins (R3).
	refs := collectReferencedPluginWords(candyMap, cfg.Box, refWords)
	if err := loadProjectPlugins(context.Background(), candyMap, refs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: plugin load: %v\n", err)
	}
	return checkRunnerContext{CandyDirs: candyDirsFromScan(candyMap)}
}

// resolveMergedDeployTree, deployNodePluginContext, and resolveDeployNodeByPath relocated to
// plugin_loader.go (#55 W3 B3, beside loadDeployPlugins): deployNodePluginContext is plugin-LOADER
// infrastructure — its real significance is as loadDeployPlugins' (plugin_loader.go) direct input,
// not a check-only concern despite the filename it used to share a file with. See plugin_loader.go's
// header on loadDeployPlugins for the FLOOR-M clause. resolveCheckRunnerContext (below) still calls
// deployNodePluginContext directly — same package, different file, zero behavior change.

// checkLocalDeployScope/runLocalDeployScopePlan relocated to candy/plugin-fleet (#55 W3 B3,
// verify_local.go's verifyLocalDeployScope/localDeployScopePlan): the "target: local --verify"
// path they served (charly/unified_targets.go's Add) already dispatches to candy/plugin-fleet on
// EVERY call with opts.Verify/req.HasLifecycle already on the wire (spec.LifecycleOptsFromEmit),
// so the check-verify pass now runs INSIDE that same dispatch, over the SAME venue, in the SAME
// RPC round-trip — reaching command:check via a direct InvokeProvider(command,"check") call
// instead of core's former in-proc reverse-channel plumbing (redundant with the sanctioned wire
// shape — command:check's own verifyChecksForHost re-materializes its executor purely from the
// request body's Venue field, confirmed by reading candy/plugin-check/verify_checks.go). No new
// seam was needed for the template-plan lookup either: candy/plugin-fleet's OWN node_resolve.go
// already carried lookupLocalTemplate, a fully plugin-native resolver (the resolved-project
// envelope + a direct InvokeProvider(kind,"local") call, no LoadUnified) — reusing it (R3) is what
// finally makes the former core findLocalSpec/resolveLocalRefFor/namespace.go fully dead; all
// three are DELETED.

// dispatchVerifyChecks (the core-side function that drove command:check's OpVerifyChecks in-proc)
// is GONE (#55 W3 B3 remainder): its production callers are all dead now — verify_local.go
// (candy/plugin-fleet) reaches command:check via a direct sdk.Executor.InvokeProvider call
// instead (a peer plugin, not core), and runUnifiedTargetChecks/Test (unified_targets.go) had
// zero real callers of their own. The function's exact body
// relocated to checkrun_helpers_test.go — its only remaining role is exercising the production
// command:check seam + core's opInContext wiring from a handful of unit tests.

// containerImageRef + containerImage (the live-container image-ref
// inspectors) live in commands.go — ONE inspect implementation shared by
// mcp / service / remove / start-direct and the check runner.
