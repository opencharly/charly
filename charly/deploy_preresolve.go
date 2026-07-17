package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

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
// Populated at package-var init time (before any init(), like registerDedicatedBuiltin),
// so the lookup is race-free.
var (
	deployPreresolversMu sync.RWMutex
	deployPreresolvers   = map[string]deployPreresolver{}
	// pluginPreresolverWords tracks which preresolvers are WIRE-BACKED (plugin-registered at
	// load), so registerPluginDeployPreresolver may REPLACE one on reconnect but never shadow a
	// compiled-in body (k8s/android).
	pluginPreresolverWords = map[string]bool{}
)

// registerDeployPreresolver records one COMPILED-IN substrate's preresolver (k8s/android). Panics
// on a duplicate word (a startup invariant, like the registry's duplicate-provider panic).
func registerDeployPreresolver(word string, fn deployPreresolver) {
	if word == "" || fn == nil {
		panic("registerDeployPreresolver: empty word or nil preresolver")
	}
	deployPreresolversMu.Lock()
	defer deployPreresolversMu.Unlock()
	if _, dup := deployPreresolvers[word]; dup {
		panic(fmt.Sprintf("registerDeployPreresolver: duplicate preresolver for %q", word))
	}
	deployPreresolvers[word] = fn
}

// registerPluginDeployPreresolver records a WIRE-BACKED preresolver for an external deploy
// substrate at plugin-load (F6), idempotently: a plugin reconnect REPLACES the prior wire-backed
// body, but it never SHADOWS a compiled-in preresolver (k8s/android). The mirror of
// registerPluginSubstrateLifecycle.
func registerPluginDeployPreresolver(word string, fn deployPreresolver) {
	if word == "" || fn == nil {
		return
	}
	deployPreresolversMu.Lock()
	defer deployPreresolversMu.Unlock()
	if _, ok := deployPreresolvers[word]; ok && !pluginPreresolverWords[word] {
		return // a compiled-in preresolver owns this word — never shadow it
	}
	deployPreresolvers[word] = fn
	pluginPreresolverWords[word] = true
}

// wireDeployPreresolver builds a wire-backed preresolver that Invokes the plugin's OpPreresolve and
// ships the returned opaque JSON in DeployVenue.Substrate — the generalization of the in-core
// k8s/android preresolvers (F6).
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
		res, err := gp.Invoke(context.Background(), &Operation{Reserved: gp.word, Op: sdk.OpPreresolve, Params: pj})
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
