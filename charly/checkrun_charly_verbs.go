package main

import "github.com/opencharly/sdk/kit"

// checkrun_charly_verbs.go holds the shared host-side helpers the EXTERNAL
// live-container check verbs (cdp/wl/vnc/dbus/mcp/record/kube/adb/appium/spice/libvirt)
// rely on. Every live-container verb is now served OUT-OF-PROCESS by its plugin
// (candy/plugin-*) and dispatches via invokeVerbProvider (the Invoke envelope) — the
// former compiled-in live-verb subprocess dispatcher + its in-proc method-contract seam
// were DELETED once the externalization orphaned them. The post-run artifact validators
// moved to the SDK (sdk.RunArtifactValidators), the ONE copy every out-of-tree verb
// plugin calls. What remains here is the small host-side surface a marshalled plugin
// cannot compute for itself.

// resolveCheckApk is the thin hostVerbResolver wrapper around the shared
// kit.ResolveCommittedApk (CHECK-cone move, sdk/kit/apk_path.go) — anchoring a relative
// committed-APK path against the AUTHORING candy's source tree, so a check resolves its
// fixture whether the candy is local OR fetched via @github. This wrapper's sole job is
// supplying the two host-only inputs the pure resolver needs: h.cc.CandyDirs() (from
// ScanAllCandyWithConfig + candyDirsFromScan, check_cmd.go) and h.cc.CandyScanErr().
func (h *hostVerbResolver) resolveCheckApk(apk, origin string) (string, error) {
	return kit.ResolveCommittedApk(apk, origin, h.cc.CandyDirs(), h.cc.CandyScanErr())
}

// noVmDisplayDeviceErr is the substring the VM-target resolver (charly/vm_target.go)
// emits when a VM declares no graphics device of the requested kind ("VM <name> has
// no SPICE/VNC graphics device declared in vm.yml") — the signal for a legitimate N/A
// SKIP, not a check failure. Both VM-display verbs are EXTERNAL-CHARLY-VERBS
// (candy/plugin-spice, candy/plugin-vnc); the skip is enforced HOST-side by their
// endpoint resolution (spice AND vnc via the cc.ResolveGraphicsEndpoint reverse-leg's Skip)
// and the
// skip wording stays anchored to ONE string (R3).
const noVmDisplayDeviceErr = "graphics device declared in vm.yml"
