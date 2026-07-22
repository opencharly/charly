package main

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// sidecar.go — the HOST side of the `sidecar` kind after the sidecar de-type
// (Cutover D). ALL sidecar BUSINESS LOGIC — the embedded+project+deploy template
// merge, CLI env-flag routing, and volume/secret-name + env_from resolution — lives
// in candy/plugin-sidecar's OpResolve leg. The host holds only OPAQUE sidecar bodies
// (map[string]json.RawMessage) and consumes the resolved ResolvedSidecar values this
// file's adapter builds; the quadlet/naming helpers below are pure host machinery.

// ResolvedSidecar (the host-adapted, generation-ready sidecar form the sidecar plugin's
// spec.ResolvedSidecar wire type is adapted into) is a deploykit resolved-runtime type
// now, referenced directly as deploykit.ResolvedSidecar — it moved to sdk/deploykit with
// the pod config-write mechanism (P11), since its CollectedSecret/VolumeMount/SecurityConfig
// fields are all deploykit/vmshared types.

// resolveSidecarsViaPlugin invokes candy/plugin-sidecar's OpResolve leg — the single
// point where sidecar defs are resolved. The host passes OPAQUE def layers + the CLI
// env; the plugin returns generation-ready sidecars, the app-only env, and the routed
// deploy overrides to persist. The kernel reads no spec.Sidecar fields.
func resolveSidecarsViaPlugin(in spec.SidecarResolveInput) (spec.SidecarResolveReply, error) {
	reply, err := hostInvoke[spec.SidecarResolveInput, spec.SidecarResolveReply](ClassKind, "sidecar", OpResolve, in)
	if err != nil {
		return spec.SidecarResolveReply{}, fmt.Errorf("sidecar resolve: %w", err)
	}
	return reply, nil
}

// resolvedSidecarFromSpec adapts one plugin-resolved spec.ResolvedSidecar into the
// host's ResolvedSidecar (the quadlet-gen shape).
func resolvedSidecarFromSpec(s spec.ResolvedSidecar) deploykit.ResolvedSidecar {
	rs := deploykit.ResolvedSidecar{Name: s.Name, Image: s.Image, Env: s.Env}
	if s.Security != nil {
		rs.Security = *s.Security
	}
	for _, v := range s.Volume {
		rs.Volume = append(rs.Volume, deploykit.VolumeMount(v))
	}
	for _, sec := range s.Secret {
		rs.Secret = append(rs.Secret, deploykit.CollectedSecret{
			Name:       sec.Name,
			Env:        sec.Env,
			HostEnv:    sec.HostEnv,
			SecretName: sec.SecretName,
		})
	}
	return rs
}

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
