package main

// shell_profile.go — host-side shell profile integration.
//
// On `charly bundle add host`, each installed candy contributes a set of
// env vars and PATH additions (from the candy manifest's env: + path_append:).
// These are materialized as `~/.config/opencharly/env.d/<candy>.env` files and
// sourced via a managed block in the user's shell init — both handled by
// sdk/kit/profile.go now (DetectShellFromPath/RenderEnvdBody/EnvdFilePath/
// EnvdDir/ManagedBlockBody/ShellInitFilePath/MarkersForTag), consumed by
// kit.WalkPlans since the local/vm deploy targets externalized.
//
// TRACKED P13-KERNEL EXIT (DEPLOY-wave W2 audit, 2026-07-20; R5-swept 2026-07-20
// Cutover B unit 3+4): every OTHER function this file used to carry
// (DetectLoginShell/WriteEnvdFile/ManagedBlockBody/ShellInitFilePath/
// markersForTag/renderEnvdBody/shQuoteEnv/shDoubleQuotePath/getShellFromPasswd,
// the charly-local EnvdFilePath/EnvdDir wrappers, and the ShellKind type) was
// confirmed DEAD in production (verified by grep: zero non-test callers each)
// and deleted — the sdk/kit/profile.go equivalents already own every one of
// those concerns; deploy_target_external.go's ONE remaining caller (the
// ShellHookStep.EnvFile default) now calls kit.EnvdFilePath directly instead
// of this file's now-removed duplicate. host_infra_test.go's coverage of the
// deleted functions was trimmed/repointed to kit.* in the SAME change (R5).
//
// What remains is the ONE genuinely non-portable leaf: RemoveEnvdFile, wired
// into sdk/deploykit's injected-seam package var below (mirroring
// deploykit.CompileServiceSteps) because deploykit cannot import charly core.
// Whether this leaf itself could instead be wired from a plugin's own init()
// (deploykit is already a plugin-importable sdk package) is an open FINAL/K5
// question, not resolved here — the wiring pattern doesn't require charly
// core specifically, but re-homing it is a seam-ownership decision left to
// that wave.

import (
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

func init() { deploykit.RemoveEnvdFile = RemoveEnvdFile }

// RemoveEnvdFile deletes an env.d entry. Silently succeeds when absent.
func RemoveEnvdFile(hostHome, candyName string) error {
	err := os.Remove(kit.EnvdFilePath(hostHome, candyName))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
