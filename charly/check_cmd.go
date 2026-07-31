package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/report"
	specexec "github.com/opencharly/spec/exec"
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
// the sdk (sdk.CheckFailExitCode / sdk.CheckSkippedExitCode); the plugin/main signal it
// across the module boundary via *sdk.ExitCodeError. The `charly check` CLI + its
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
func resolveCheckRunnerContext(box, dir string, cfg *Config) checkRunnerContext {
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

// resolveMergedDeployTree returns the top-level Bundle (deploy-node) map — the merged project
// charly.yml + per-host operator overlay, ready for dotted-path traversal — the host-side
// merged-tree read the two remaining check host seams need (deployNodePluginContext below +
// check_venue_resolve.go's checkVenueExecFromReply). It replaces the DELETED deploy_tree.go
// host merged-tree read (#55 LOADER cone): instead of a host-resident sdk/deploykit projection+merge
// (the incomplete seam a floor-M read must not carry), it drives the LOADER CAPABILITY —
// loaderkit.ResolveMergedTreeViaExecutor over an in-proc host reverse channel (the SAME
// executorReverseServer path command:validate / command:bundle drive) — so the deploykit
// projection/overlay/merge lives INSIDE loaderkit, off charly core, and this read routes through
// the loader broker exactly like every Cone A Unit 3 dispatch reader. The in-proc executor reaches
// only the compiled-in loader-* host legs (it never runs the
// deploy-plugins-connect seam), so a PRE-CONNECT caller (deployNodePluginContext feeding
// loadDeployPlugins BEFORE any out-of-process plugin connects) never recurses.
//
// TRACKED-GATED loaderkit import (#55 coneA): ResolveMergedTreeViaExecutor does the per-host
// operator-overlay merge (loaderkit.LoadHostBundleConfigViaExecutor + MergeDeployConfigs) that the
// spec.ProjectLoader.LoadUnified seam does NOT expose (LoadUnified returns the PROJECT-only tree,
// loadmodel.go Bundle has no overlay field), so repointing to LoadUnified would DROP operator
// overrides (verified — not byte-equivalent). Named exit: the resolveMergedDeployTree envelope
// (a new ProjectLoader seam method returning the merged tree, OR the K1 loader-floor envelope) —
// in-flight, NOT permanent floor; when it lands, check_cmd sheds loaderkit (call the seam, not
// loaderkit). NOT the boundary-law "host-boundary-object" trap: the merge IS a loader mechanism
// the plugin can drive; the shed is blocked only until the seam exposes it.
func resolveMergedDeployTree(dir string) (map[string]spec.BundleNode, error) {
	ex := sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}})
	return loaderkit.ResolveMergedTreeViaExecutor(context.Background(), ex, dir)
}

// deployNodePluginContext resolves the deploy/bed node named `name` in the project at
// `dir` ONCE (the SAME project-bundle loader the deploy walker uses) and returns the
// two plugin-loading inputs the check runner (resolveCheckRunnerContext) and the deploy
// path (loadDeployPlugins) both need (R3 — one helper, both paths):
//
//   - addCandy: the deploy's `add_candy:` refs. The project candy scan
//     (ScanAllCandyWithConfig) collects only IMAGE-closure candies (CollectRemoteRefs
//     walks base/builder/require edges); add_candy candies are NOT in that set, so both
//     callers feed these to ScanAllCandyWithConfigOpts' ExtraCandyRefs to fetch them.
//   - refWords: the plugin WORDS the node references DIRECTLY — its substrate kind (an
//     external deploy-substrate plugin word, e.g. `exampledeploy`) + every inline
//     Op.Plugin in its FLATTENED plan. flattenBundleVenues hoists member/nested steps
//     into the root node.Plan, so this ONE walk covers the whole bed including members
//     (e.g. a `spice:` check verb authored inline). These scope loadProjectPlugins to
//     the plugins the deploy actually dispatches — caught here because they appear in
//     NEITHER a candy plan NOR a box plan (over-load safe, never under-load).
//
// Best-effort: (nil, nil) on any load failure or unknown name (the caller still
// collects candy + box references; a genuinely missing reference fails loudly at
// dispatch, never silently mis-deploys).
func deployNodePluginContext(dir, name string) (addCandy []string, refWords []string) {
	tree, err := resolveMergedDeployTree(dir)
	if err != nil || tree == nil {
		return nil, nil
	}
	// Resolve the named node, walking a DOTTED path into nested children (the bed runner
	// deploys a nested child via `charly bundle add <root>.<child>` — its name is dotted and
	// is NOT a top-level tree key). Without dotted resolution a nested-child deploy surfaces
	// NO plugin words and its substrate word never loads its provider (ResolveTarget →
	// "unknown target"). The single source for "given a (possibly dotted) deploy name, which
	// node?".
	node, ok := resolveDeployNodeByPath(tree, name)
	if !ok {
		return nil, nil
	}
	inSubmodule := selfSuperprojectOverridePair(dir) != ""
	// Collect the node's plugin words AND recurse into its nested children: a deploy whose
	// OWN substrate OR whose nested children's substrates are externalized must load each
	// serving plugin. Two cases this covers, GENERALLY (never substrate-special-cased):
	//   - a dotted child deploy (check-arch-vm.arch-host) — node IS the nested child, so its
	//     OWN target (e.g. `local`) is surfaced + its plugin auto-injected;
	//   - a single-process tree deploy (a pod root walked in one process, its nested children
	//     of a DIFFERENT substrate) — the recursion surfaces every child's substrate word.
	var visit func(n *spec.BundleNode)
	visit = func(n *spec.BundleNode) {
		if n == nil {
			return
		}
		addCandy = append(addCandy, n.AddCandy...)
		if n.Target != "" {
			refWords = append(refWords, n.Target)
			// An EXTERNALIZED deploy substrate (vm/local/android/k8s) is served by an
			// out-of-process plugin candy. A main-repo project discovers that candy from
			// candy/ directly (its `discover:` scans candy/*), but a box/<distro> SUBMODULE
			// scans only its own + imported candies — so the parent's
			// candy/plugin-deploy-<substrate> is absent from the submodule's scan and the
			// substrate word would never resolve to its provider. Auto-inject the canonical
			// ref via ExtraCandyRefs, but ONLY in a submodule context — the main repo already
			// has it locally, and injecting a remote ref there over the local candy is both
			// redundant and (for an as-yet-unpublished plugin) a fetch failure. In a submodule
			// bed CHARLY_REPO_OVERRIDE redirects the ref to the local superproject under
			// development. The SAME host-side-plugin pattern as vmPluginCandyRef (verb:libvirt),
			// generalized to every external substrate (R3).
			if inSubmodule {
				if ref, ok := externalDeploySubstratePluginRef(n.Target); ok {
					addCandy = append(addCandy, ref)
				}
			}
		}
		for i := range n.Plan {
			op := &n.Plan[i].Op
			if w := op.Plugin; w != "" {
				refWords = append(refWords, w)
			}
			// Also surface each step's VERB discriminator. A closed-#Op EXTERNAL check verb
			// (libvirt/spice/kube/adb/appium) is NOT a `plugin:` word, so without this the
			// loader never build-connects the out-of-process plugin candy serving it — e.g. a
			// bed's `libvirt: list` step would SKIP with "unknown verb". Over-load safe: a
			// compiled-in verb's candy is already registered, and a non-plugin verb has none.
			if v, err := op.Kind(); err == nil && v != "" {
				refWords = append(refWords, v)
			}
		}
		for _, ck := range spec.SortedNestedKeys(n.Children) {
			visit(n.Children[ck])
		}
	}
	visit(node)
	// NOTE: the externalized DETECTION-builder plugins (cargo/npm/pixi/aur) are NOT injected here.
	// A builder is triggered by the DEPLOY's resolved image closure (a pixi.toml / aur: section), not
	// by the deploy NODE this walk sees — and surfacing all four across a whole-box scan over-built
	// unrelated builder plugins (aur on a fedora deploy). The build PRE-PASS (builder_preresolve.go)
	// instead detects EXACTLY the builders the deploy triggers (distro-gated) and connects only those
	// on-demand, by their canonical ref (ensureBuildersConnected), where it has the resolved closure.
	return addCandy, refWords
}

// resolveDeployNodeByPath resolves a (possibly DOTTED) deploy name to its BundleNode,
// descending node.Children for each dotted segment (the SAME nested-tree shape
// ResolveDeployChain walks). A bare name is the top-level entry; a dotted name
// (root.child[.grandchild…]) is the nested child the bed runner deploys via `charly bundle
// add <root>.<child>`. A leading "vm:" is stripped first via spec.SplitVmAddress (RCA #8/#9,
// FINAL/K5 unit 6a, live-probe-caught) — the SAME legacy-vm CLI-addressing convention
// resolveDelNode / spec.VmNameFromDeployName already honor elsewhere (`charly bundle del vm:<name>`
// / `vm:<parent.child>`): without stripping it, `tree["vm:"+parts[0]]` never matches (the tree
// is keyed by the plain name), so a "vm:"-prefixed dotted address silently resolved to
// nothing here — deployNodePluginContext (this function's one caller) then collected ZERO
// referenced plugin words for the deploy, and its substrate provider was never connected by
// loadDeployPlugins. resolveDelNode's OWN "vm:"-prefix shortcut masked the miss (it returns a
// synthetic Target-only placeholder without touching the tree at all), so the del RESOLVED
// fine while the CONNECT silently failed — the gap surfaced only later, when dispatch needed
// the never-connected provider. Returns false when any segment is absent.
func resolveDeployNodeByPath(tree map[string]spec.BundleNode, name string) (*spec.BundleNode, bool) {
	name, _ = spec.SplitVmAddress(name)
	parts := strings.Split(name, ".")
	root, ok := tree[parts[0]]
	if !ok {
		return nil, false
	}
	cur := &root
	for _, seg := range parts[1:] {
		child, ok := cur.Children[seg]
		if !ok || child == nil {
			return nil, false
		}
		cur = child
	}
	return cur, true
}

// checkLocalDeployScope collects a local deployment's deploy-scope checks —
// kind:local template `check:` (base) merged with the deploy entry `check:`
// (extends/overrides) and the per-host charly.yml overlay — and runs them on
// `exec`. Used by `charly bundle add <local> --verify` (the local deploy target);
// `charly check live <local>` now runs plugin-side (candy/plugin-check/live_gather.go's
// pluginCheckLiveLocal), sourcing the SAME plan shape off the resolved-project envelope. Host-
// context vars only (no HOST_PORT:<N> / CONTAINER_IP). Returns the failure count.
func checkLocalDeployScope(dir string, node *spec.BundleNode, image, instance, _ string, _ []string, exec spec.DeployExecutor, format string) (int, error) { //nolint:unparam // error return kept for symmetry with sibling deploy-scope checks
	results, hadPlan, err := runLocalDeployScopePlan(dir, node, image, instance, exec)
	if err != nil {
		return 0, err
	}
	if !hadPlan {
		fmt.Fprintln(os.Stderr, "No plan steps to run.")
		return 0, nil
	}
	return report.ReportStepResultsCount(os.Stdout, results, format), nil
}

// runLocalDeployScopePlan collects a local deployment's deploy-scope plan — the kind:local
// template `check:` (base) + the deploy node `check:` — and runs it on exec, returning the
// per-step results. hadPlan is false when there were no plan steps (the caller prints its own
// "no plan" line). CLI-free core shared by checkLocalDeployScope (the external local deploy
// --verify path, reporting to os.Stdout) — the check-live CLI counterpart now runs plugin-side
// (pluginRunLocalDeployScopePlan, candy/plugin-check/live_gather.go). Host-context vars only
// (no HOST_PORT:<N> / CONTAINER_IP). Folds the ${HOST} CloseHosts teardown (design §6): the
// ssh -L forwards a VM-peer subject opens are torn down after the plan run.
//
// The per-host charly.yml OVERLAY merge (the deploy-entry `check:` extends/overrides) moved
// PLUGIN-SIDE (#55 CHECK-ENGINE cone Option A — candy/plugin-check/verify_checks.go's
// verifyChecksRunPlan reads the per-host deploy config via sdk/deploykit itself, so the core
// `target: local` --verify path imports zero deploykit). What STAYS core is the kind:local
// template + deploy-node plan ASSEMBLY (findLocalSpec + node.Plan) — the base plan threaded to
// the plugin, which appends the overlay entry's plan before driving RunPlan.
func runLocalDeployScopePlan(dir string, node *spec.BundleNode, image, instance string, exec spec.DeployExecutor) (results []spec.StepResult, hadPlan bool, err error) {
	var plan []spec.Step
	if node != nil && strings.TrimSpace(node.From) != "" {
		if spec, _ := findLocalSpec(dir, strings.TrimSpace(node.From)); spec != nil {
			plan = append(plan, spec.Plan...)
		}
	}
	if node != nil {
		plan = append(plan, node.Plan...)
	}
	if len(plan) == 0 {
		return nil, false, nil
	}
	// The RunPlan-DRIVE + the per-host overlay merge run PLUGIN-SIDE (command:check OpVerifyChecks,
	// #55 CHECK-ENGINE cone Unit 2): the plugin rebuilds the host-context env (USER/HOME via the
	// venue's ResolveHome), the ${HOST:<member>} host-vars, and the cross-deployment TargetResolver
	// from {dir, box, instance} — none of which cross the wire (plugin-check already does this for
	// check-live) — and appends the per-host overlay entry's plan before driving RunPlan. What STAYS
	// core is exactly this base-plan ASSEMBLY (kind:local template via findLocalSpec + deploy node).
	results, err = dispatchVerifyChecks(context.Background(), exec, spec.VerifyChecksRequest{
		Plan: plan, Mode: "live", Box: image, Instance: instance, VerifyOnly: true, Dir: dir,
	})
	if err != nil {
		return nil, true, err
	}
	return results, true, nil
}

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
	invokeCtx := sdk.ContextWithExecutor(ctx,
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{exec: exec}}))
	res, err := prov.Invoke(invokeCtx, &Operation{Reserved: "check", Op: sdk.OpVerifyChecks, Params: reqJSON})
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
