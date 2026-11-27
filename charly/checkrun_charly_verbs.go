package main

// checkrun_charly_verbs.go holds the shared host-side helpers the EXTERNAL
// live-container check verbs (cdp/wl/vnc/dbus/mcp/record/kube/spice/libvirt)
// rely on. Every live-container verb is now served OUT-OF-PROCESS by its plugin
// (candy/plugin-*) and dispatches via invokeVerbProvider (the Invoke envelope). There is no
// compiled-in live-verb subprocess dispatcher and no in-proc method-contract seam here: the
// post-run artifact validators live in the SDK (sdk.RunArtifactValidators), the ONE copy every
// out-of-tree verb plugin calls. What remains here is the small host-side surface a marshalled
// plugin
// cannot compute for itself.

// noVmDisplayDeviceErr is the substring the VM-target resolver (charly/vm_target.go)
// emits when a VM declares no graphics device of the requested kind ("VM <name> has
// no SPICE/VNC graphics device declared in vm.yml") — the signal for a legitimate N/A
// SKIP, not a check failure. Both VM-display verbs are EXTERNAL-CHARLY-VERBS
// (candy/plugin-spice, candy/plugin-vnc); the skip is enforced HOST-side by their
// endpoint resolution (spice AND vnc via the cc.ResolveGraphicsEndpoint reverse-leg's Skip)
// and the
// skip wording stays anchored to ONE string (R3).
const noVmDisplayDeviceErr = "graphics device declared in vm.yml"
