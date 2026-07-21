package main

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk"
)

// deploy_preresolve.go — the GENERAL per-substrate deploy preresolver hook (F1).
//
// An external (out-of-process) deploy substrate's plugin runs the deployment on a
// venue it cannot hold across the process boundary. For most substrates the generic
// externalDeployTarget hands the plugin the deploy's InstallPlan VIEWS + a venue
// descriptor and the plugin drives the venue via the E3b reverse channel. But some
// substrates need HOST-RESOLVED inputs the InstallPlanView provenance view cannot
// carry (the rich Steps are not on the wire) — e.g. deploy:android needs the live adb
// endpoint (engine inspect on the running pod) + the apk install specs (committed-APK
// paths resolved against the candy source tree) + the google-play creds (host
// credential store). That resolution is substrate-specific AND requires host context,
// so it CANNOT live in the plugin and MUST NOT android-special-case the generic target.
//
// The hook is the seam: each external substrate registers ONE preresolver keyed by its
// word; externalDeployTarget looks it up and, when present, ships its opaque payload in
// DeployVenue.Substrate. The generic target never branches on the substrate — only the
// preresolver body is substrate-specific. GENERAL for all 5: any substrate that needs
// host-resolved venue data registers a preresolver here.

// deployPreresolver resolves the substrate-specific preresolved venue payload for one
// external deploy. It receives the deploy's identity (name/path), the project dir, the
// dispatch-merged node (may be nil on the Update path — the preresolver re-resolves
// from the tree), and the compiled InstallPlans (where the apk: ApkInstallStep entries
// live). It returns the opaque JSON the matching plugin decodes (or nil to ship none).
type deployPreresolver func(name, dir string, node *spec.BundleNode, plans []*spec.InstallPlan) (json.RawMessage, error)

// deployPreresolvers maps an external deploy SUBSTRATE word → its host-side preresolver.
// Every entry today is WIRE-BACKED (F6, unit 6a): android and k8s — the only two
// substrates that ever needed this hook — moved their preresolve bodies to
// candy/plugin-adb and candy/plugin-kube, so registerPluginDeployPreresolver is
// currently the SOLE writer. The map (and pluginPreresolverWords, tracking which
// entries are wire-backed) stay keyed generically, not android/k8s-specific, so a
// future substrate needing host-resolved venue data can register through the same
// seam without a new mechanism.
var (
	deployPreresolversMu   sync.RWMutex
	deployPreresolvers     = map[string]deployPreresolver{}
	pluginPreresolverWords = map[string]bool{}
)

// registerPluginDeployPreresolver records a WIRE-BACKED preresolver for an external deploy
// substrate at plugin-load (F6), idempotently: a plugin reconnect REPLACES the prior
// wire-backed body for the same word. The mirror of registerPluginSubstrateLifecycle.
func registerPluginDeployPreresolver(word string, fn deployPreresolver) {
	if word == "" || fn == nil {
		return
	}
	deployPreresolversMu.Lock()
	defer deployPreresolversMu.Unlock()
	deployPreresolvers[word] = fn
	pluginPreresolverWords[word] = true
}

// wireDeployPreresolver builds a wire-backed preresolver that Invokes the plugin's OpPreresolve and
// ships the returned opaque JSON in DeployVenue.Substrate — the generalization of the in-core
// k8s/android preresolvers (F6). Dispatches WITH a reverse-channel broker (kit.ShellExecutor{}, the
// SAME "no live venue, just give me HostBuild access" idiom host_build_pod_config.go's
// invokePodConfigOp already uses) — a moved preresolve BODY reaches the "deploy-entity-resolve"
// HostBuild seam (unit 6a) for its LoadUnified-coupled lookups, which a plain gp.Invoke (no
// broker) could not serve.
func wireDeployPreresolver(gp *grpcProvider) deployPreresolver {
	return func(name, dir string, node *spec.BundleNode, plans []*spec.InstallPlan) (json.RawMessage, error) {
		var extra map[string]any
		if len(plans) > 0 {
			extra = map[string]any{"plans": plans}
		}
		pj, err := marshalDeployOpParams(name, dir, node, extra)
		if err != nil {
			return nil, err
		}
		res, err := gp.InvokeWithExecutor(context.Background(),
			&Operation{Reserved: gp.word, Op: sdk.OpPreresolve, Params: pj},
			kit.ShellExecutor{}, buildEngineContext{}, false, nil)
		if err != nil {
			return nil, err
		}
		return res.JSON, nil
	}
}

// deployPreresolverFor returns the registered preresolver for an external substrate
// word, if any. externalDeployTarget calls it; a substrate with no preresolver (the
// marker-only example) ships an empty DeployVenue.Substrate.
func deployPreresolverFor(word string) (deployPreresolver, bool) {
	deployPreresolversMu.RLock()
	defer deployPreresolversMu.RUnlock()
	fn, ok := deployPreresolvers[word]
	return fn, ok
}
