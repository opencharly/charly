package main

import (
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// DeployTargetProvider is the typed in-process form of a deploy-target Provider:
// it resolves a BundleNode to the UnifiedDeployTarget that adds/dels/updates it.
// Every built-in target (local/vm/pod/k8s/android) implements it; ResolveTarget
// resolves the node's derived target word through providerRegistry and calls
// ResolveTarget — the legacy-alias normalization + the dispatch switch are gone (C3).
type DeployTargetProvider interface {
	Provider
	ResolveTarget(node *spec.BundleNode, name string) (UnifiedDeployTarget, error)
}

// deployTargetWords is the canonical deploy-target set (the cross-ref-inferred
// node.Target values). Every word is also a kind (a deploy target is a deployable
// kind), so the bijection ties this list to the CUE kind vocabulary — it cannot
// drift from spec.KindWords — AND asserts each is served EITHER by an in-proc
// DeployTargetProvider OR by an external out-of-process plugin (externalizedDeploySubstrates).
var deployTargetWords = []string{"local", "vm", "pod", "k8s", "android"}

// externalizedDeploySubstrates is THE single source of truth for which canonical
// deploy-substrate kinds are served by an EXTERNAL out-of-process plugin instead
// of a compiled-in DeployTargetProvider (F1 — the substrate-kind-plugin dispatch
// seam). A word listed here has NO in-proc builtin: its grpcProvider registers at
// plugin-load time and ResolveTarget (unified_targets.go) routes target:<word> to
// the generic pluginDeployTarget (S3b), a thin data-only proxy that dispatches
// EVERY verb (Add/Del/Test/Update/Start/Stop/Status/Logs/Shell/Attach/Rebuild) to
// candy/plugin-bundle's Invoke(OpDeployDispatch), which reaches the substrate's own
// out-of-process provider via sdk.Executor.InvokeProvider — never a direct E3b call
// from core. Both checkDeployProviderBijection (in-proc XOR externalized) and
// isExternalDeploySubstrate (a substrate kind is external iff listed here) consult
// it — so the two gates can never disagree. GENERAL for all 5 — ALL FIVE substrates
// now externalize; the ONLY substrate-specific piece is each one's registered
// preresolver body (F6, FINAL/K5 unit 6a — candy/plugin-adb/preresolve.go /
// candy/plugin-kube/preresolve.go, dispatched by candy/plugin-bundle's
// preresolveSubstrate via InvokeProvider(OpPreresolve), S3b — the core-side
// deploy_preresolve.go:wireDeployPreresolver registry it used to route through is
// dissolved, since the caller is itself a plugin now) OR lifecycle hook
// (lifecycleStartPlanHooks/lifecycleStopPlanHooks/lifecycleAttachPlanHooks,
// pod_lifecycle_dispatch.go — pod only; vm registers none, see below), never a
// branch in the generic dispatch. local needs NEITHER — its plan walk + executor
// selection are the generic pluginDeployTarget path (the executor is Shell for
// host:local, SSH for host:user@machine — see ResolveTarget), so the plan VIEWS
// the host marshals already carry everything the candy/plugin-deploy-local plugin
// needs.
//
// vm is served by candy/plugin-deploy-vm (kit.WalkPlans over the GUEST SSHExecutor).
// Unlike local/android/k8s it owns a real venue LIFECYCLE, implemented ENTIRELY
// in the plugin (candy/plugin-deploy-vm/lifecycle.go): boots the domain, builds
// the guest SSHExecutor the reverse channel serves, runs the nested pod-in-guest
// orchestration, and owns Start/Stop/Status/Logs/Shell/Rebuild — reached the SAME
// generic way as every other substrate (pluginDeployTarget → OpDeployDispatch →
// InvokeProvider), no separate core-side substrateLifecycle registry. The
// arbiter-claim bracket around vm's own `charly vm start`/`stop` reentry is vm's
// OWN concern (never double-bracketed by arbiter_bracket.go, which is pod-scoped
// only — see its doc comment); the ssh-config / charly.yml-entry / ephemeral
// teardown bookkeeping is the vm's own hostBuildConfigPersist writer
// (charly/vm_deploy_state.go).
//
// pod is served by candy/plugin-deploy-pod, but unlike vm its plugin WALKS NOTHING: pod bakes
// its install steps INTO the image at build time, so its PrepareVenue (podPrepareVenue) builds
// the overlay container image HOST-SIDE via HostBuild("overlay") → the core prep+resolve seam
// (build_overlay.go) + the candy's own deploykit.OCITarget render, and owns the container
// lifecycle (config/start/remove + the `charly update` rebuild gate) — reached the same generic
// OpDeployDispatch path, with its Start/Stop/Attach further routing through pod_lifecycle_dispatch.go's
// registered plan hooks (arbiter-bracketed by arbiter_bracket.go, S3b). The prep+resolve stays
// core, the render is in the candy.
var externalizedDeploySubstrates = map[string]bool{
	"android": true,
	"k8s":     true,
	"local":   true,
	"pod":     true,
	"vm":      true,
}

// externalDeploySubstratePlugins maps each first-party EXTERNALIZED deploy-substrate word
// to the candy SUBPATH of the plugin that serves it (in the default project repo). It is the
// substrate→plugin-candy companion of externalizedDeploySubstrates: that set says a word is
// external; this map says WHICH candy serves it.
var externalDeploySubstratePlugins = map[string]string{
	"local":   "candy/plugin-deploy-local",
	"vm":      "candy/plugin-deploy-vm",
	"pod":     "candy/plugin-deploy-pod",
	"android": "candy/plugin-adb",
	"k8s":     "candy/plugin-kube",
}

// externalDeploySubstratePluginRef returns the canonical @github ref to the candy serving an
// externalized deploy SUBSTRATE word, and whether the word is a first-party externalized
// substrate. A box/<distro> SUBMODULE's beds reference the substrate plugin nowhere in their
// own candy closure — a main-repo project discovers it from candy/ directly (its `discover:`
// scans candy/*), but a submodule scans only its own + imported candies — so the deploy/check
// plugin-load paths auto-inject this ref (via ExtraCandyRefs) ONLY in a submodule context, so
// the substrate word resolves to its out-of-process provider. In a submodule bed
// CHARLY_REPO_OVERRIDE redirects it to the local superproject under development — the SAME
// host-side-plugin pattern as vmPluginCandyRef for verb:libvirt (vm_plugin_client.go, R3).
func externalDeploySubstratePluginRef(word string) (string, bool) {
	sub, ok := externalDeploySubstratePlugins[word]
	if !ok {
		return "", false
	}
	return "@" + spec.DefaultProjectRepo + "/" + sub, true
}

// checkDeployProviderBijection: every canonical deploy-target word is a valid deployable
// kind (⊆ spec.ResourceKinds — the "word is known" invariant; the substrate kinds
// pod/vm/k8s/local/android are ResourceKinds even after C2-substrate dropped them from
// KindWords, since they are externalized to candy/plugin-substrate and no longer carry a
// #Node arm) AND is served by EXACTLY ONE of {an in-proc DeployTargetProvider, an external
// plugin (externalizedDeploySubstrates)} — never both (XOR), never neither. Run in the same
// init() that registers (after registration), avoiding the alphabetical race. An externalized
// word legitimately has NO provider at process start (its grpcProvider connects later at load).
func checkDeployProviderBijection() error {
	kinds := map[string]bool{}
	for _, k := range spec.ResourceKinds {
		kinds[k] = true
	}
	var problems []string
	for _, w := range deployTargetWords {
		if !kinds[w] {
			problems = append(problems, w+" (not a spec.ResourceKinds kind)")
		}
		p, hasBuiltin := providerRegistry.resolve(ClassDeployTarget, w)
		ext := externalizedDeploySubstrates[w]
		switch {
		case ext && hasBuiltin:
			problems = append(problems, w+" (externalized substrate must NOT also have an in-proc DeployTargetProvider)")
		case ext && !hasBuiltin:
			// OK — served out-of-process by an external plugin connected at load time. It MUST
			// also name its canonical plugin candy so a box/<distro> submodule can auto-inject
			// the ref (externalDeploySubstratePluginRef) and resolve the substrate word.
			if _, ok := externalDeploySubstratePlugins[w]; !ok {
				problems = append(problems, w+" (externalized substrate has no externalDeploySubstratePlugins entry — a submodule can't discover its plugin candy)")
			}
		case !ext && !hasBuiltin:
			problems = append(problems, w+" (no DeployTargetProvider and not an externalized substrate)")
		default: // !ext && hasBuiltin
			if _, ok := p.(DeployTargetProvider); !ok {
				problems = append(problems, w+" (registered but not a DeployTargetProvider)")
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("reserved-word registry: deploy-target provider bijection broken: %v", problems)
	}
	return nil
}
