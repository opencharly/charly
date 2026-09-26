package main

// externalizedDeploySubstrates is the set of deploy words served by a plugin — DERIVED
// from the generated provider-ref index (pluginProviderRefs, the projection of every
// plugin repo's own `plugin:` block), never from a compiled-in per-kind map or a closed
// vocabulary (boundary-law clause D). A word in this set has NO in-proc builtin: its
// provider registers at plugin-load time (or is declared by a project), and ResolveTarget
// (unified_targets.go) routes target:<word> to the generic pluginDeployTarget (S3b), a
// thin data-only proxy that dispatches EVERY verb (Add/Del/Test/Update/Start/Stop/
// Status/Logs/Shell/Attach/Rebuild) to candy/plugin-fleet's Invoke(OpDeployDispatch),
// which reaches the substrate's own out-of-process provider via
// sdk.Executor.InvokeProvider — never a direct call from core.
//
// It is OPEN by construction: the deploy provider set is whatever plugins declare. A
// canonical substrate (deploy:pod/vm/kubernetes/kubevirt/kindcluster/local/android), a
// plugin-only example target (deploy:exampledeploy), and a third party's own word
// (declared by its project) are all the same kind of fact — a plugin serves the word.
// This is why the former `checkDeployProviderBijection` is gone: there is no closed deploy
// vocabulary to check a plugin-declared word against, so a plugin can add a deploy target
// with ZERO core edits.
//
// Per-substrate behaviour is DECLARED, never a branch here: each plugin reports its own
// #DeployTraits (P9), so deployTraitsFor reads the traits off the resolved provider; a
// substrate's preresolve/lifecycle legs are its own provider's InvokeProvider ops
// (candy/plugin-adb + candy/plugin-kube/kubevirt register preresolve; candy/plugin-deploy-vm
// and candy/plugin-deploy-pod own lifecycles) — reached the SAME generic way as every other
// substrate, with no separate core-side registry.
var externalizedDeploySubstrates = deploySubstrateWords()

// deploySubstrateWords projects the generated provider-ref index onto the deploy class —
// the DEPLOY subset of "which plugin serves which word". A mutable map (the
// reserved_registry test's delete/restore probe still works). It reuses splitCapability (R3:
// the ONE key-grammar parser) rather than a second parser.
func deploySubstrateWords() map[string]bool {
	out := map[string]bool{}
	for key := range pluginProviderRefs {
		class, word, _, ok := splitCapability(key)
		if ok && class == ClassDeployTarget {
			out[word] = true
		}
	}
	return out
}

// pluginProviderRef returns the canonical candy ref for a capability identity
// "<class>:<word>[:<parent>]", read from the GENERATED provider-ref index. THE one place a
// word resolves to a plugin ref (boundary-law clause D: kind-recognition Data consulted by
// word, never a compiled-in per-kind Go map). Shared by every class — a substrate
// (deploy:vm) and a verb (verb:libvirt) resolve through the same table, no special-casing.
func pluginProviderRef(key string) (string, bool) {
	ref, ok := pluginProviderRefs[key]
	if !ok || ref == "" {
		return "", false
	}
	return ref, true
}
