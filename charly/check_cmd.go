package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
)

// check_cmd.go — the residual host-side check-project plumbing after the K1-unblock wave's "live"
// arm moved to candy/plugin-check/live_gather.go (pluginCheckRunLive, wired via
// host_build_check_run.go's Mode:"live" short-circuit in candy/plugin-check/command.go). What
// remains here is used by the STILL-host-resident arms (feature-live via host_build_check_run.go's
// hostFeatureLive, the new "check-load-plugins" seam in host_build_check_load_plugins.go) and by
// the external `target: local` deploy's own --verify path (unified_targets.go), which are NOT part
// of the "live" check-run mode and stay core for now.

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
	candyMap, scanErr := ScanAllCandyWithConfigOpts(dir, cfg, ResolveOpts{ExtraCandyRefs: addCandy})
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
	tree, err := resolveTreeRoot(dir)
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
		for _, ck := range deploykit.SortedNestedKeys(n.Children) {
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
// add <root>.<child>`. A leading "vm:" is stripped first via vmshared.SplitVmAddress (RCA #8/#9,
// FINAL/K5 unit 6a, live-probe-caught) — the SAME legacy-vm CLI-addressing convention
// resolveDelNode / vmshared.VmNameFromDeployName already honor elsewhere (`charly bundle del vm:<name>`
// / `vm:<parent.child>`): without stripping it, `tree["vm:"+parts[0]]` never matches (the tree
// is keyed by the plain name), so a "vm:"-prefixed dotted address silently resolved to
// nothing here — deployNodePluginContext (this function's one caller) then collected ZERO
// referenced plugin words for the deploy, and its substrate provider was never connected by
// loadDeployPlugins. resolveDelNode's OWN "vm:"-prefix shortcut masked the miss (it returns a
// synthetic Target-only placeholder without touching the tree at all), so the del RESOLVED
// fine while the CONNECT silently failed — the gap surfaced only later, when dispatch needed
// the never-connected provider. Returns false when any segment is absent.
func resolveDeployNodeByPath(tree map[string]spec.BundleNode, name string) (*spec.BundleNode, bool) {
	name, _ = vmshared.SplitVmAddress(name)
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
func checkLocalDeployScope(dir string, node *spec.BundleNode, image, instance, _ string, _ []string, exec deploykit.DeployExecutor, format string) (int, error) { //nolint:unparam // error return kept for symmetry with sibling deploy-scope checks
	results, hadPlan, err := runLocalDeployScopePlan(dir, node, image, instance, exec)
	if err != nil {
		return 0, err
	}
	if !hadPlan {
		fmt.Fprintln(os.Stderr, "No plan steps to run.")
		return 0, nil
	}
	return kit.ReportStepResultsCount(os.Stdout, results, format), nil
}

// runLocalDeployScopePlan collects a local deployment's deploy-scope plan — the kind:local
// template `check:` (base) + the deploy node `check:` + the per-host overlay — and runs it on
// exec, returning the per-step results. hadPlan is false when there were no plan steps (the
// caller prints its own "no plan" line). CLI-free core shared by checkLocalDeployScope (the
// external local deploy --verify path, reporting to os.Stdout) — the check-live CLI counterpart
// now runs plugin-side (pluginRunLocalDeployScopePlan, candy/plugin-check/live_gather.go). Host-
// context vars only (no HOST_PORT:<N> / CONTAINER_IP). Folds the ${HOST} CloseHosts teardown
// (design §6): the ssh -L forwards a VM-peer subject opens are torn down after the plan run.
func runLocalDeployScopePlan(dir string, node *spec.BundleNode, image, instance string, exec deploykit.DeployExecutor) (results []kit.StepResult, hadPlan bool, err error) { //nolint:unparam // err kept for symmetry; RunPlan never errors here today
	var plan []spec.Step
	if node != nil && strings.TrimSpace(node.From) != "" {
		if spec, _ := findLocalSpec(dir, strings.TrimSpace(node.From)); spec != nil {
			plan = append(plan, spec.Plan...)
		}
	}
	if node != nil {
		plan = append(plan, node.Plan...)
	}
	if dc := deploykit.LoadDeployConfigForRead("charly check live"); dc != nil {
		if entry, ok := dc.Bundle[deploykit.DeployKey(image, instance)]; ok {
			plan = append(plan, entry.Plan...)
		} else if entry, ok := dc.Bundle[image]; ok {
			plan = append(plan, entry.Plan...)
		}
	}

	user := os.Getenv("USER")
	home, herr := exec.ResolveHome(context.Background(), user)
	if herr != nil || home == "" {
		home = os.Getenv("HOME")
	}
	resolver := newRuntimeCheckVarResolver(map[string]string{
		"IMAGE":    image,
		"INSTANCE": instance,
		"USER":     user,
		"HOME":     home,
	})

	if len(plan) == 0 {
		return nil, false, nil
	}
	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{Origin: "local:" + image, Plan: plan}}}
	env, hasRuntime := resolverEnv(resolver)
	// Generic cross-deployment support (on: driver + ${HOST:<member>}) — a local SUBJECT bed
	// can drive a peer too (R3). Capture + defer-close the ssh -L cleanups (design §6 leak fix):
	// the pre-P12 local path discarded them, so a local subject driving a VM peer via
	// ${HOST:<member>:<port>} leaked the forward.
	hostVars, hostCleanups := resolveHostVarsForSteps(plan, instance)
	defer kit.CloseHostCleanups(hostCleanups)
	runner := newCheckRunner(kit.RunnerConfig{
		Exec:           exec,
		Mode:           RunModeLive,
		Env:            env,
		HasRuntime:     hasRuntime,
		Box:            image,
		Instance:       instance,
		VerifyOnly:     true,
		HostVars:       hostVars,
		TargetResolver: venueResolver(instance),
	})
	return kit.RunPlan(context.Background(), runner, set, false), true, nil
}

// containerImageRef + containerImage (the live-container image-ref
// inspectors) live in commands.go — ONE inspect implementation shared by
// mcp / service / remove / start-direct and the check runner.
