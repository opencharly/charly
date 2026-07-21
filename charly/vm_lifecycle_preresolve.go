package main

// vm_lifecycle_preresolve.go — the host-side hooks the externalized vm substrate lifecycle STILL
// needs from core (FINAL/K5 unit 6a, M4b): the F12 attach-resolver (a live-session script the
// plugin cannot derive itself) and the ephemeral post-teardown cleanup (systemd transient timers +
// libvirt snapshot refcounts — un-importable by the plugin). The PrepareVenue DATA-seam
// (lifecyclePrepareHook) that used to live here is GONE (hard cutover): candy/plugin-deploy-vm now
// owns its OWN OpPrepareVenue resolution end to end, self-serving any LoadUnified-coupled data via
// the generic "deploy-entity-resolve" HostBuild seam — see candy/plugin-deploy-vm/lifecycle.go's
// vmPrepareVenue, the reference every future Lifecycle:true substrate's own prepare body follows.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// vmAttachResolver builds the F12 #PodLiveStdioPlan for `charly shell <vm-deploy>` / `charly cmd
// <vm-deploy>`: the vm's live venue executor is the guest *SSHExecutor (grpcSubstrateLifecycle.Attach
// resolves it via the vm VenueExecutor), whose RunInteractive wraps the script in `ssh -t <alias>
// [script]`. So the resolved script is just the in-guest command — the user's cmd joined, or empty for
// a bare interactive login shell (matching `charly vm ssh <alias>`). tty is immaterial for ssh (`-t`
// is always allocated on the interactive leg); the shell-vs-cmd distinction is a container concept.
func vmAttachResolver(_ context.Context, _, _ string, cmd []string, _ bool) (json.RawMessage, error) {
	return marshalJSON(&spec.PodLiveStdioPlan{Script: strings.Join(cmd, " ")})
}

var _ = func() bool { registerLifecycleLivePlanHooks("vm", vmAttachResolver, nil); return true }()

// lifecyclePostTeardownHook runs host-side substrate cleanup AFTER the plugin's OpPostTeardown that
// the plugin cannot do (it uses core-only machinery). Registered per word; the proxy consults it.
type lifecyclePostTeardownHook func(name string, node *spec.BundleNode) error

var lifecyclePostTeardownHooks = map[string]lifecyclePostTeardownHook{}

func registerLifecyclePostTeardownHook(word string, fn lifecyclePostTeardownHook) {
	if word == "" || fn == nil {
		panic("registerLifecyclePostTeardownHook: empty word or nil fn")
	}
	if _, dup := lifecyclePostTeardownHooks[word]; dup {
		panic(fmt.Sprintf("registerLifecyclePostTeardownHook: duplicate hook for %q", word))
	}
	lifecyclePostTeardownHooks[word] = fn
}

func lifecyclePostTeardownHookFor(word string) (lifecyclePostTeardownHook, bool) {
	fn, ok := lifecyclePostTeardownHooks[word]
	return fn, ok
}

var _ = func() bool { registerLifecyclePostTeardownHook("vm", vmLifecyclePostTeardown); return true }()

// vmLifecyclePostTeardown runs the vm ephemeral-lifecycle teardown host-side (systemd transient
// timers + libvirt snapshot refcounts — un-importable by the plugin). The ssh-config stanza + the
// charly.yml entry removal are the plugin's job (kit.RemoveVmSshStanza + PostTeardownReply.RemoveEntries).
func vmLifecyclePostTeardown(name string, node *spec.BundleNode) error {
	// RCA #9 (FINAL/K5 unit 6a, live-probe-caught): LookupKey is a bare exact dc.Bundle[key]
	// match (sdk/deploykit) — it does NOT accept the raw deploy name in either its "vm:"-prefixed
	// CLI-address form or its plain dotted form. Every vm writer (RCA #2/#6/#7's
	// ephemeralOverlayKey / saveVmDeployState) persists under the ONE canonical
	// "vm:"+VmDomainIdentity(name) key — the sanitized, dash-joined form, e.g.
	// "vm:check-sidecar-pod-check-sidecar-pod-ephvm" for a dotted nested deploy. Looking up by
	// `name` directly missed that key entirely, so a genuinely-registered ephemeral entry was
	// never found here and TeardownEphemeralLifecycle never fired. VmDomainIdentity strips a
	// "vm:" prefix internally too, so this is correct whether or not RCA #9's other fix
	// (hostBuildDeployNodeDelDispatch's name normalization) has already stripped it upstream.
	key := "vm:" + vmshared.VmDomainIdentity(name)
	// RCA #9 finding #10 (FINAL/K5 unit 6a, caught by this fix's OWN required test):
	// dcNode.IsEphemeral() checks Deploy.Ephemeral != nil — the AUTHORED ephemeral: {ttl: ...}
	// TTL declaration (sdk/spec/cue_types_gen.go) — never set on a per-host overlay entry BY
	// DESIGN (ephemeralFallbackNode, candy/plugin-bundle/ephemeral.go, seeds only Target/From:
	// "an overlay entry is state, never structure"). The RUNTIME record this function actually
	// needs lives at dcNode.VmState.Ephemeral (*spec.EphemeralRuntime), a DIFFERENT field this
	// condition never checked — so TeardownEphemeralLifecycle has NEVER fired from this call
	// site, for any key form, before or after the canonical-key fix above. Matches the pattern
	// every OTHER ephemeral-family function already uses (e.g. bumpParentChildRefcount's
	// `node.VmState == nil || node.VmState.Ephemeral == nil`).
	if dcNode, ok := deploykit.LoadDeployConfigForRead("vm ephemeral-teardown").LookupKey(key); ok && dcNode.VmState != nil && dcNode.VmState.Ephemeral != nil {
		return TeardownEphemeralLifecycle(&dcNode, name)
	}
	return nil
}
