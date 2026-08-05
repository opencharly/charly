package main

// vm_lifecycle_preresolve.go — the ONE host-side hook the externalized vm substrate lifecycle
// STILL needs from core (FINAL/K5 unit 6a, M4b; F6 vm-lifecycle move, coneB-vmlifecycle): the F12
// attach-resolver (a live-session script the plugin cannot derive itself). Everything else that
// used to live here is GONE:
//   - The PrepareVenue DATA-seam (lifecyclePrepareHook) — candy/plugin-deploy-vm owns its OWN
//     OpPrepareVenue resolution end to end, self-loading the project directly now (K-wave W3a
//     A3-phase-2: sdk/loaderkit.ResolveVmEntityViaExecutor, candy/plugin-deploy-vm/lifecycle.go's
//     vmPrepareVenue) — no HostBuild seam remains for this leg.
//   - vmLifecyclePostTeardown + the lifecyclePostTeardownHook registry (F6 vm-lifecycle move):
//     the "un-importable by the plugin" framing this file's header used to carry was STALE — the
//     actual ephemeral-teardown WORK (systemd transient timers, libvirt snapshot refcounts) was
//     already 100% plugin-side (candy/plugin-bundle's teardownEphemeral, reached over
//     OpEphemeralTeardown); this file's vmLifecyclePostTeardown was only a thin read-then-dispatch
//     wrapper around the SAME peer-dispatch a plugin can do itself via exec.InvokeProvider. It now
//     lives in candy/plugin-deploy-vm/lifecycle.go's vmPostTeardown, resolving the persisted
//     VmState via the existing "config-resolve" seam (resolvePriorVmState) instead of a direct
//     deploykit.LoadDeployConfigForRead call (this plugin runs out-of-process — see that file's
//     resolvePriorVmState doc for why the direct call would silently no-op). The registry
//     (registerLifecyclePostTeardownHook/lifecyclePostTeardownHookFor) had exactly one registrant
//     (vm) and is deleted as dead code (R5) along with its unified_targets.go call site; core's
//     TeardownEphemeralLifecycle (then in ephemeral_dispatch.go) lost its sole caller and was
//     deleted too. The OpEphemeralRegister dispatch that remained has since ALSO moved plugin-side
//     (candy/plugin-deploy-vm's dispatchVmEphemeralRegister, the teardown twin's mirror) — the whole
//     ephemeral_dispatch.go + host_build_ephemeral_register.go host round-trip is retired.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/opencharly/spec/spec"
)

// vmAttachResolver builds the F12 #PodLiveStdioPlan for `charly shell <vm-deploy>` / `charly cmd
// <vm-deploy>`: the vm's live venue executor is the guest *SSHExecutor (pluginDeployTarget's Attach
// resolves it via the vm VenueExecutor), whose RunInteractive wraps the script in `ssh -t <alias>
// [script]`. So the resolved script is just the in-guest command — the user's cmd joined, or empty for
// a bare interactive login shell (matching `charly vm ssh <alias>`). tty is immaterial for ssh (`-t`
// is always allocated on the interactive leg); the shell-vs-cmd distinction is a container concept.
func vmAttachResolver(_ context.Context, _, _ string, cmd []string, _ bool) (json.RawMessage, error) {
	return marshalJSON(&spec.PodLiveStdioPlan{Script: strings.Join(cmd, " ")})
}

var _ = func() bool { registerLifecycleLivePlanHooks("vm", vmAttachResolver, nil); return true }()
