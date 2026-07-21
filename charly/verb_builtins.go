package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// The remaining IN-CHARLY-MODULE built-in check verb as a CheckVerbProvider (the
// generic plugin dispatcher below). An internal/dispatch verb, NOT a user-authored
// plugin: blocks; each wraps its r.runX handler, and runOne resolves them via
// providerRegistry.ResolveVerb.
//
// Every OTHER verb has been relocated to a COMPILED-IN candy (candy/plugin-<name>):
//   - The goss-tier + state-provision verbs (process/port/dns/http/interface/addr/matching/
//     file/user/unix_group/kernel-param/mount/command/package/service) — RegisterBuiltinPluginUnit
//     candies. `package`/`service` are TypedStepProviders (their act lowers to a
//     SystemPackagesStep / ServicePackagedStep with load-bearing reversals via the host's
//     materializeStep — the one piece that stays in package main); file/user/unix_group/
//     kernel-param/mount are ProvisionActors; `command` is the install-task emitCmd branch.
//   - NO compiled-in live-container verb remains: `wl` (the last one) externalized into
//     candy/plugin-wl, an out-of-process plugin (below). After it externalized, the
//     compiled-in live-verb seam was deleted (the live-verb externalization orphaned it);
//     every live-container verb now dispatches out-of-process via invokeVerbProvider.
// All relocated verbs are absent from builtinProviderInstances + the `providers:` manifest;
// they dispatch identically through providerRegistry. kube/adb/appium/spice are the four
// out-of-process external-charly-verbs (below).

// kube is NOT a built-in verb — it is an EXTERNAL-CHARLY-VERB served out-of-process by
// candy/plugin-kube (the third dep-shed: the client-go + apimachinery stack
// left charly's core go.mod). It keeps its `kube:` discriminator + modifiers on core #Op
// (authoring unchanged) but is NOT a CheckVerbProvider, so it dispatches via
// invokeVerbProvider (the else-branch in runOne) once the loader registers its grpcProvider
// — never through this in-proc set. The plugin resolves any --cluster profile to a
// concrete kubeconfig context via the cc.ResolveClusterContext reverse-leg; the same plugin's
// clientcmd-backed k3s post-provision finalization (kubeconfig rewrite + merge) routes
// through it via k8s_plugin.go's invokeKubePluginWithBroker.

// adb is NOT a built-in verb — it is an EXTERNAL-CHARLY-VERB served out-of-process by
// candy/plugin-adb (the second dep-shed: the goadb ADB-wire dependency left charly's
// core go.mod). It keeps its `adb:` discriminator + modifiers on core #Op (authoring
// unchanged) but is NOT a CheckVerbProvider, so it dispatches via invokeVerbProvider (the
// else-branch in runOne) once the loader registers its grpcProvider — never through this
// in-proc set. The SAME plugin also serves the `deploy:android` SUBSTRATE (the
// externalized `target: android` deploy, F1) and the goadb-backed status device probe,
// so ALL Android device interaction — verb, deploy, status — lives in candy/plugin-adb.

// appium is NOT a built-in verb — it is an EXTERNAL-CHARLY-VERB served out-of-process by
// candy/plugin-appium (the first dep-shed: tebeka/selenium left charly's core go.mod). It
// keeps its `appium:` discriminator + modifiers on core #Op (authoring unchanged) but is
// NOT a CheckVerbProvider, so it dispatches via invokeVerbProvider (the else-branch in
// runOne) once the loader registers its grpcProvider — never through this in-proc set.

// spice is NOT a built-in verb — it is an EXTERNAL-CHARLY-VERB served out-of-process by
// candy/plugin-spice (the fourth dep-shed: the upstream SPICE wire client library + its
// cgo opus/portaudio audio transitives left charly's core go.mod). It keeps its `spice:`
// discriminator + modifiers on core #Op (authoring unchanged) but is NOT a
// CheckVerbProvider, so it dispatches via invokeVerbProvider (the else-branch in runOne)
// once the loader registers its grpcProvider — never through this in-proc set. The plugin
// resolves the VM's live SPICE endpoint to a dialable address via the cc.ResolveGraphicsEndpoint
// reverse-leg, so it needs no go-libvirt.

// pluginVerb — the generic `plugin:` discriminator. Its RunVerb resolves the
// authored plugin word (op.Plugin) to its registered Provider and Invokes it
// (the out-of-proc / built-in plugin verb). See runPluginVerb.
type pluginVerb struct{ builtinVerbBase }

func (pluginVerb) Reserved() string { return "plugin" }
func (pluginVerb) RunVerb(ctx context.Context, h *hostVerbResolver, op *spec.Op) spec.CheckResult {
	return h.runPluginVerb(ctx, op)
}
