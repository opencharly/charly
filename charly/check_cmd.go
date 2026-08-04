package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// check_cmd.go — the residual host-side check-project plumbing after the K1-unblock wave's "live"
// and "feature-live" arms moved to candy/plugin-check (live_gather.go's pluginCheckRunLive,
// feature_run_gather.go's pluginCheckRunFeatureLive — both wired via command.go's Mode
// short-circuit; "feature-box" was traced and never had a live caller through this seam — see
// feature_run_gather.go's header; "feature-box" is now the plugin-side pluginCheckRunFeatureBox,
// reached from candy/plugin-box's command:feature over InvokeProvider — cone-C #31). What remains
// here is used by the new "check-load-plugins" seam (host_build_check_load_plugins.go) and by the
// external `target: local` deploy's own --verify path (unified_targets.go) — neither is part of the
// "live"/"feature-live" check-run modes reached via the check-run seam, so they stay core.

// The `charly check` exit-code contract (2 = checks failed, 3 = prereq skip) lives in
// the sdk (exitcode.CheckFailExitCode / exitcode.CheckSkippedExitCode); the plugin/main signal it
// across the module boundary via *exitcode.ExitCodeError. The `charly check` CLI + its
// exit-code plumbing live in command:check (candy/plugin-check).

// candyDirsFromScan extracts the candy-name → SourceDir map from a scanned candy
// set. Keyed by the candy MAP KEY — the check's Origin form: a bare name for a
// local candy ("sshd"), the bare @github ref for a fetched one
// ("github.com/owner/repo/candy/<name>"). CollectDescriptions stamps
// Origin = "candy:" + this same key, so resolveCheckApk's CandyDirs[origin]
// lookup matches in BOTH cases. The SAME scanned map drives the plugin loader
// (R3 — one scan, both consumers).
func candyDirsFromScan(candyMap map[string]spec.CandyReader) map[string]string {
	if len(candyMap) == 0 {
		return nil
	}
	out := make(map[string]string, len(candyMap))
	for key, lyr := range candyMap {
		if lyr != nil && lyr.GetSourceDir() != "" {
			out[key] = lyr.GetSourceDir()
		}
	}
	return out
}

// checkRunnerContext carries the committed-APK anchoring (CandyDirs / CandyScanErr) a live
// baked-plan runner folds into its RunnerConfig. resolveCheckRunnerContext computes it (and
// performs the plugin-load side effect); the caller wires the fields into kit.RunnerConfig.
type checkRunnerContext struct {
	CandyDirs    map[string]string
	CandyScanErr error
}

// resolveCheckRunnerContext computes the committed-APK anchoring + loads the OUT-OF-TREE plugin
// candies a live baked-plan runner needs, so `charly check live` and `charly check feature run`
// resolve adb/appium `apk:` checks IDENTICALLY (R3). They previously diverged — only check live
// populated CandyDirs, so a committed-APK check passed under check live yet failed to anchor
// ("0 candies scanned") under feature run. Any RunModeLive runner that executes a baked plan
// MUST fold its result into the RunnerConfig (CandyDirs + CandyScanErr).
func resolveCheckRunnerContext(box, dir string, cfg *spec.Config) checkRunnerContext {
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

// checkLocalDeployScope/runLocalDeployScopePlan relocated to candy/plugin-bundle (#55 W3 B3,
// verify_local.go's verifyLocalDeployScope/localDeployScopePlan): the "target: local --verify"
// path they served (charly/unified_targets.go's Add) already dispatches to candy/plugin-bundle on
// EVERY call with opts.Verify/req.HasLifecycle already on the wire (spec.LifecycleOptsFromEmit),
// so the check-verify pass now runs INSIDE that same dispatch, over the SAME venue, in the SAME
// RPC round-trip — reaching command:check via a direct InvokeProvider(command,"check") call
// instead of core's former in-proc reverse-channel plumbing (redundant with the sanctioned wire
// shape — command:check's own verifyChecksForHost re-materializes its executor purely from the
// request body's Venue field, confirmed by reading candy/plugin-check/verify_checks.go). No new
// seam was needed for the template-plan lookup either: candy/plugin-bundle's OWN node_resolve.go
// already carried lookupLocalTemplate, a fully plugin-native resolver (the resolved-project
// envelope + a direct InvokeProvider(kind,"local") call, no LoadUnified) — reusing it (R3) is what
// finally makes the former core findLocalSpec/resolveLocalRefFor/namespace.go fully dead; all
// three are DELETED.

// dispatchVerifyChecks drives a deploy-scope check pass PLUGIN-SIDE via command:check's
// OpVerifyChecks (#55 CHECK-ENGINE cone Unit 2). The host holds a live venue executor but no longer
// builds the in-proc kit.Runner — that construction (the former checkrun.go newCheckRunner +
// planrun_adapter.go venueResolver) moved into candy/plugin-check, shedding both files' sdk/kit
// imports. A live executor cannot cross the wire, so it is flattened to a spec.VenueDescriptor
// (specexec.DescriptorFromExecutor) the plugin re-materializes via kit.VenueFromDescriptor — the SAME
// mechanism candy/plugin-bundle's resolveRootExecutor uses. An in-proc reverse channel is threaded
// (the deploy_target_dispatch.go / check_venue_resolve.go idiom) so the plugin's own verb-dispatch
// (InvokeProvider) + local-verify resolvedProject (HostBuild) legs reach the host. The reply is the
// sanctioned []spec.StepResult wire (byte-identical to the former sdk/kit []StepResult — the
// DeadlineExceeded engine flag is json:"-", so spec.StepResult and kit.StepResult share one wire).
func dispatchVerifyChecks(ctx context.Context, exec spec.DeployExecutor, req spec.VerifyChecksRequest) ([]spec.StepResult, error) {
	prov, ok := providerRegistry.resolve(ClassCommand, "check")
	if !ok {
		return nil, fmt.Errorf("verify-checks: command:check provider not loaded (candy/plugin-check must be compiled in via compiled_plugins:)")
	}
	req.Venue = specexec.DescriptorFromExecutor(exec)
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("verify-checks: marshal request: %w", err)
	}
	invokeCtx := specexec.ContextWithExecutor(ctx,
		specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{exec: exec}}))
	res, err := prov.Invoke(invokeCtx, &Operation{Reserved: "check", Op: ops.OpVerifyChecks, Params: reqJSON})
	if err != nil {
		return nil, fmt.Errorf("verify-checks: command:check plugin: %w", err)
	}
	var out []spec.StepResult
	if res != nil && len(res.JSON) > 0 {
		if uerr := json.Unmarshal(res.JSON, &out); uerr != nil {
			return nil, fmt.Errorf("verify-checks: decode reply: %w", uerr)
		}
	}
	return out, nil
}

// containerImageRef + containerImage (the live-container image-ref
// inspectors) live in commands.go — ONE inspect implementation shared by
// mcp / service / remove / start-direct and the check runner.
