package main

// deploy_add_shared.go — generic helpers shared across the per-kind
// UnifiedDeployTarget.Add methods (R3). Each one captures a step that
// was copy-pasted across the old per-kind deploy bodies; now there is
// ONE implementation, called from local/vm/pod Add.
//
// Ordering is load-bearing and preserved exactly:
//   - secrets are injected into the plans BEFORE any Emit (a candy's
//     OpStep body references the resolved token via env).
//   - artifactEnv is secretEnv first, then node.Env lines overlaid
//     (last-wins) — so a deploy entry's explicit env: overrides an
//     auto-generated secret of the same name.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// prepareCandySecrets resolves the candies backing `plans`, computes their
// secret_requires / secret_accepts env (auto-generating + persisting any
// missing required token), and injects it into every plan's TaskSteps
// BEFORE emission. Returns the resolved candy list (the caller reuses it
// for artifact retricheck) and the secret env map.
//
// Shared by the external substrate apply path AND each lifecycle hook's PrepareVenue
// (vm: before the in-guest walk; pod: before the host-side overlay build) — the paths that
// previously each ran CandyForPlan + ResolveSecretForCandy + InjectSecretsIntoPlans inline.
func prepareCandySecrets(plans []*deploykit.InstallPlan, dir string) ([]spec.CandyReader, map[string]string, error) {
	candyList, err := CandyForPlan(plans, dir, nil)
	if err != nil {
		return nil, nil, err
	}
	secretEnv := ResolveSecretForCandy(candyList)
	deploykit.InjectSecretsIntoPlans(plans, secretEnv)
	return candyList, secretEnv, nil
}

// loadDeployPlugins connects the project's OUT-OF-TREE plugin candies BEFORE a
// deploy verb resolves the target, so a deploy whose SUBSTRATE / step / verb is
// served by an external provider resolves out-of-process. It scans the WHOLE
// project (ScanAllCandyWithConfigOpts) but loads ONLY the plugin candies the
// deployment REFERENCES (perf-scoped): collectReferencedPluginWords unions the
// candy/box plans + candy external_builder selections, and deployNodePluginContext
// adds the deploy's OWN references — its substrate kind + the inline Op.Plugin words
// in its FLATTENED bed plan (members hoisted into the root node.Plan). A plugin candy
// none of whose providers is referenced is skipped (no wasted host build/connect); a
// REFERENCED one always loads (the reference set is collected COMPLETE — over-load
// safe, never under). The deployment's add_candy: candies + any caller-supplied extra
// refs are ADDED to the scan via ExtraCandyRefs (so a REMOTE composed plugin not in
// the local scan is fetched too, and its words are then collected from its plan). The
// SAME scan + loadProjectPlugins the check runner uses (resolveCheckRunnerContext) and
// the bundle-add path uses — so bundle add / bundle del / charly update all connect a
// deployment's plugins identically (R3). For an external deploy SUBSTRATE this is what
// turns the pre-scanned placeholder word into a connected grpcProvider that
// ResolveTarget can route to. Discovery and build/connect failures retain their original cause and
// abort dispatch; warning-and-continue used to mask a failed build as a downstream missing provider.
func loadDeployPlugins(dir, deployName string, extraAddCandy []string) error {
	cfg, cerr := LoadConfig(dir)
	if cerr != nil {
		return fmt.Errorf("load plugin configuration: %w", cerr)
	}
	addCandy, refWords := deployNodePluginContext(dir, deployName)
	extra := append(append([]string(nil), extraAddCandy...), addCandy...)
	candyMap, scanErr := ScanAllCandyWithConfigOpts(dir, cfg, ResolveOpts{ExtraCandyRefs: extra})
	if scanErr != nil {
		return fmt.Errorf("scan deploy plugins: %w", scanErr)
	}
	if candyMap == nil {
		return nil
	}
	refs := collectReferencedPluginWords(candyMap, cfg.Box, refWords)
	if perr := loadProjectPlugins(context.Background(), candyMap, refs); perr != nil {
		return fmt.Errorf("load deploy plugins: %w", perr)
	}
	return nil
}

// artifactRegisterHandlers maps a candy artifact's declared `register:` hint (the
// #vmshared.CandyArtifact.Register field, SDD-sourced in sdk/schema/candy.cue) to the
// post-retrieve processing it triggers. Word-keyed and data-driven (R3): a candy
// declares the hint on its OWN artifact entry (k3s-server's kubeconfig artifact
// declares `register: kubeconfig`) — adding a new registration kind means adding ONE
// map entry here, never a hardcoded candy-name special-case.
var artifactRegisterHandlers = map[string]func(artifactKey, deployName string) error{
	"kubeconfig": K3sPostProvision,
}

// retrieveArtifactsAndK3s pulls back the candies' published artifacts via the same
// executor the deploy used, then runs whichever post-retrieve registration handlers
// the retrieved candies' artifact declarations name (e.g. the k3s-server kubeconfig's
// `register: kubeconfig` — merge kubeconfig + register ClusterProfile). No-op under
// DryRun.
//
// Shared by the local deploy target.Add / the vm deploy's Add path. artifactKey is
// ENTITY-scoped (the artifact retrieve dir + the shared per-VM k3s cluster cache/context);
// deployName is the real per-deploy (domain) identity the k3s port-forward lookup keys off.
func retrieveArtifactsAndK3s(ctx context.Context, exec deploykit.DeployExecutor, candyList []spec.CandyReader, artifactKey, deployName string, artifactEnv map[string]string, opts deploykit.EmitOpts) error {
	if opts.DryRun {
		return nil
	}
	if err := deploykit.RetrieveCandyArtifacts(ctx, exec, candyList, kit.SanitizeDeployName(artifactKey), artifactEnv, opts, loadedReadiness()); err != nil {
		return err
	}
	for register := range deploykit.CandyArtifactRegisters(candyList) {
		handler, ok := artifactRegisterHandlers[register]
		if !ok {
			continue
		}
		if err := handler(artifactKey, deployName); err != nil {
			return err
		}
	}
	return nil
}

// registerEphemeralIfMarked runs the ephemeral lifecycle registration
// (systemd transient timer + parent-detection) when the dispatch-merged
// node is ephemeral. Called as the FIRST action of vm's Add (panic-safe TTL
// ordering) — the ONLY substrate that calls it today (vm_lifecycle_preresolve.go);
// pod/k8s Add never reach it (a pre-existing gap, tracked to the bed-robustness
// batch — see validate_ephemeral.go for the load-time guard that makes the gap
// LOUD instead of silent). Consumes the merged node — does NOT re-read charly.yml.
// Dispatches to command:bundle's OpEphemeralRegister (ephemeral_dispatch.go,
// FINAL/K5 unit 6a) — the registration BODY moved to candy/plugin-bundle.
//
// RCA #5 (FINAL/K5 unit 6a, live-probe-caught): an ORDINARY registration error (e.g.
// systemd-run missing — an expected condition) stays a soft, logged warning, matching the
// prior run* behavior. A PANIC-CLASS error (sdk.EphemeralPanicMarker — the plugin's
// recoverEphemeralOpPanic converts an unrecovered panic into this marker, since a bare Go
// panic previously crashed silently or vanished before reaching this caller — team-lead's
// probe caught a nil-map write panic in persistEphemeralRuntime that a bed run reported as
// PASS) is NEVER a soft warning: it signals a genuine bug, so it is returned to FAIL the
// whole Add — "a panicking registration must fail the add, not vanish."
func registerEphemeralIfMarked(node *spec.BundleNode, name string) error {
	if node == nil || !node.IsEphemeral() {
		return nil
	}
	regErr := RegisterEphemeralLifecycle(node, name)
	if regErr == nil {
		return nil
	}
	if isEphemeralPanicError(regErr) {
		return fmt.Errorf("ephemeral lifecycle registration: %w", regErr)
	}
	fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle registration: %v\n", regErr)
	return nil
}

// isEphemeralPanicError reports whether err was converted from a recovered panic (carries
// sdk.EphemeralPanicMarker — candy/plugin-bundle's recoverEphemeralOpPanic) rather than an
// ordinary registration condition. Pulled out as its own pure function purely for testability
// (registerEphemeralIfMarked's own caller, RegisterEphemeralLifecycle, is seam-coupled — needs
// the live provider registry — not unit-testable standalone).
func isEphemeralPanicError(err error) bool {
	return err != nil && strings.Contains(err.Error(), sdk.EphemeralPanicMarker)
}

// -----------------------------------------------------------------------------
// k3s post-provision dispatch (folded from the former k3s_post.go + k8s_plugin.go,
// plugin-kube #2 seam-death). Co-located with the artifactRegisterHandlers map above
// (the "kubeconfig" register hint wires here) + retrieveArtifactsAndK3s, which already
// owns k3s artifact registration — so the standalone dispatch-shim files drop.
//
// The k3s post-provision LOGIC — the retrieved-path check, the guest-forward kubeconfig
// server rewrite, and the ~/.kube/config merge — lives WHOLESALE in candy/plugin-kube
// (k3s_post.go there), reached via the SAME out-of-process kube provider the `kube:` check
// verb uses. The dispatch is WITH the reverse-channel broker (InvokeWithExecutor,
// kit.ShellExecutor{} — the "broker only, no live venue" idiom) because the plugin's
// k3s-post-provision method needs the "deploy-entity-resolve" HostBuild seam for its
// LoadUnified-coupled VM-forward lookup. The `kit` import (kit.ShellExecutor{}, the
// broker-only dispatch idiom) is UNTIL-plugin-kube-externalization — it exits once
// invokeKubePluginWithBroker's whole call chain moves into candy/plugin-kube itself.
// -----------------------------------------------------------------------------

// K3sPostProvision dispatches the k3s-post-provision method to candy/plugin-kube and prints its
// returned status line. artifactKey is the ENTITY-scoped identity (the shared per-VM cluster cache
// dir + kubeconfig context — one k3s cluster per VM); deployName is the real per-deploy (domain)
// identity the port-forward lookup keys off. A "not a k3s-server deploy" (or --dry-run-skipped)
// outcome round-trips as an EMPTY message (the plugin's own no-op case) — nothing is printed.
// This is the artifactRegisterHandlers["kubeconfig"] handler (deploy_add_shared.go's map above).
func K3sPostProvision(artifactKey, deployName string) error {
	op := &spec.Op{Plugin: "kube", PluginInput: map[string]any{
		"method": "k3s-post-provision", "artifact_key": artifactKey, "deploy_name": deployName,
	}}
	msg, err := invokeKubePluginWithBroker(op)
	if err != nil {
		return fmt.Errorf("k3s post-provision %q: %w", artifactKey, err)
	}
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	return nil
}

// K3sPostProvision's registration must match the func(artifactKey, deployName string) error shape
// artifactRegisterHandlers expects — this compile-time assertion catches a signature drift loudly.
var _ func(string, string) error = K3sPostProvision

// resolveKubePlugin lazily build-connects candy/plugin-kube if the deploy path has not already (the
// generic host-adapter seam, F7) and asserts it out-of-process (the ONE placement this plugin ships
// in — its client-go dependency is never compiled in), returning the *grpcProvider
// invokeKubePluginWithBroker needs.
func resolveKubePlugin() (*grpcProvider, error) {
	prov, ok := connectPluginByWord(ClassVerb, "kube")
	if !ok {
		return nil, fmt.Errorf("kube plugin not loaded — the deploy must compose candy/plugin-kube (its provider serves the clientcmd-backed kubeconfig merge); k3s-server requires it")
	}
	gp, ok := prov.(*grpcProvider)
	if !ok {
		return nil, fmt.Errorf("kube plugin: resolved provider is not out-of-process (%T) — the k3s deploy seam requires the external candy/plugin-kube placement", prov)
	}
	return gp, nil
}

// invokeKubePluginWithBroker dispatches a synthetic kube #Op WITH the reverse-channel broker
// (InvokeWithExecutor), so the plugin's Invoke can call back HostBuild — the k3s-post-provision
// leg. kit.ShellExecutor{} is the "broker only, no live venue" idiom (the deploy already ran on its
// own executor; this finalization step runs entirely on the operator host's own filesystem —
// ~/.cache/charly + ~/.kube/config). It is a swappable package-level var (like InspectContainer) so
// K3sPostProvision's callers stay unit-testable without a live plugin.
var invokeKubePluginWithBroker = func(op *spec.Op) (string, error) {
	prov, err := resolveKubePlugin()
	if err != nil {
		return "", err
	}
	params, err := marshalJSON(op)
	if err != nil {
		return "", fmt.Errorf("kube plugin: marshal op: %w", err)
	}
	res, err := prov.InvokeWithExecutor(context.Background(),
		&Operation{Reserved: "kube", Op: OpRun, Params: params}, kit.ShellExecutor{}, buildEngineContext{}, false, nil)
	if err != nil {
		return "", fmt.Errorf("kube plugin: %w", err)
	}
	var pr pluginCheckResult
	if res != nil && len(res.JSON) > 0 {
		if uerr := json.Unmarshal(res.JSON, &pr); uerr != nil {
			return "", fmt.Errorf("kube plugin: decode reply: %w", uerr)
		}
	}
	if pr.Status == "fail" {
		return pr.Message, fmt.Errorf("kube plugin: %s", pr.Message)
	}
	return pr.Message, nil
}
