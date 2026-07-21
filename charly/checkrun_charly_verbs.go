package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/kit"
)

// checkrun_charly_verbs.go holds the shared host-side helpers the EXTERNAL
// live-container check verbs (cdp/wl/vnc/dbus/mcp/record/kube/adb/appium/spice/libvirt)
// rely on. Every live-container verb is now served OUT-OF-PROCESS by its plugin
// (candy/plugin-*) and dispatches via invokeVerbProvider (the Invoke envelope) — the
// former compiled-in live-verb subprocess dispatcher + its in-proc method-contract seam
// were DELETED once the externalization orphaned them. The post-run artifact validators
// moved to the SDK (sdk.RunArtifactValidators), the ONE copy every out-of-tree verb
// plugin calls. What remains here is the small host-side surface a marshalled plugin
// cannot compute for itself.

// resolveCheckApk resolves a relative committed-APK path (the external adb / appium plugin's
// install / install-app `apk: ./tests/data/...`) against the AUTHORING candy's
// source tree, so a check resolves its fixture whether the candy is local OR
// fetched via @github (the SAME walk-up the deploy path uses, R3). The check's
// Origin is "candy:<key>" where <key> is the candy MAP KEY (a bare name for a
// local candy, the bare @github ref for a fetched one) — CandyDirs is keyed by
// that same key (candySourceDirs), so the single lookup matches in both cases.
//
// It FAILS HARD (returns an error) on every condition where the fixture cannot
// be anchored — a non-candy origin (the step's Origin was lost upstream), an
// absent CandyDirs entry (the candy scan failed or did not see this candy), or a
// file missing under the candy tree. There is NO fallback and NO silent
// cwd-relative pass-through: a wrong CandyDirs must surface here, not be patched
// over into a misleading downstream "no such file".
func (h *hostVerbResolver) resolveCheckApk(apk, origin string) (string, error) {
	if apk == "" || filepath.IsAbs(apk) {
		return apk, nil
	}
	key, ok := strings.CutPrefix(origin, "candy:")
	if !ok {
		return "", fmt.Errorf("committed APK %q has origin %q, not a candy origin — cannot anchor it to a candy source tree (the step's candy Origin was not propagated)", apk, origin)
	}
	candyDirs := h.kr.CandyDirs()
	dir := candyDirs[key]
	if dir == "" {
		if h.kr.CandyScanErr() != nil {
			return "", fmt.Errorf("committed APK %q (candy %q): candy source-dir scan failed: %w", apk, key, h.kr.CandyScanErr())
		}
		return "", fmt.Errorf("committed APK %q: candy %q is absent from the source scan (%d candies scanned) — cannot anchor the fixture", apk, key, len(candyDirs))
	}
	return kit.ResolveApkPath(apk, dir)
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
