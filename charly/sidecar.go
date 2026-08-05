package main

import (
	"encoding/json"
)

// sidecar.go — the residual HOST side of the `sidecar` kind: ONLY the go:embed sidecar-template
// library read. ALL sidecar business logic lives in candy/plugin-sidecar's ops.OpResolve leg; the
// resolve DISPATCH + adapter + secret-provisioning moved to candy/plugin-deploy-pod
// (sidecar_resolve.go's resolvePodSidecars, which InvokeProviders kind:sidecar itself — this cone's
// seam-death, retiring the former resolveSidecarsViaPlugin + resolvedSidecarFromSpec + the fat
// pod-config-resolve-sidecars seam). embeddedSidecarBodies STAYS core: the go:embed data lives only
// in the charly binary, served to the plugin via the thin list-sidecars seam's BodiesJSON.
//
// The host-adapted generation-ready sidecar form is deploykit.ResolvedSidecar (moved to sdk/deploykit
// with the pod config-write mechanism, P11).

// embeddedSidecarBodies returns the binary-embedded sidecar-template library (the
// charly.yml `sidecar:` section) as OPAQUE bodies, read through the unified loader.
// It is the deploy-time template base the sidecar plugin merges under a deploy's
// own overrides.
func embeddedSidecarBodies() (map[string]json.RawMessage, error) {
	def, err := embeddedDefaults()
	if err != nil {
		return nil, err
	}
	return def.PluginKinds["sidecar"], nil
}

// findPodSidecarQuadlets DELETED (Cutover B unit 2, R1 dead-code catch surfaced while touching
// this file for the sidecarConfigDir duplicate below): zero production callers anywhere in
// charly/*.go — only its own now-deleted test exercised it. It scanned quadlet FILES for a
// load-bearing `Pod=<podName>.pod` directive to enumerate sidecars; the actual production sweep
// (charly remove's former quadlet-mode teardown, now candy/plugin-pod/remove_orchestration.go)
// enumerates sidecar names from charly.yml directly via resolveSidecarNames instead — an earlier,
// superseded approach that was never swept when its replacement landed.

// sidecarConfigDir DELETED (Cutover B unit 2, R1 divergence caught mid-flight): a byte-identical
// duplicate of the ALREADY-EXISTING sdk/kit.SidecarConfigDir (kit/sidecar_naming.go), whose own
// header comment falsely claimed "callers now import kit directly" — the SAME false-claim pattern
// as the containerRunning/containerImage divergences already on record this cutover (a third
// instance). Its only caller, the former podRemoveCmd's quadlet-mode sidecar-config-file sweep,
// moved to candy/plugin-pod/remove_orchestration.go and now calls kit.SidecarConfigDir() directly.
