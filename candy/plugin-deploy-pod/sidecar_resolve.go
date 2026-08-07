package deploypod

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// sidecar_resolve.go — the pod-config sidecar resolve+adapt+secret-provision, relocated from
// charly/host_build_pod_config_seams.go's hostBuildPodConfigResolveSidecars (seam-death).
// The former fat "pod-config-resolve-sidecars" HostBuild seam is RETIRED; the THIN
// "pod-config-list-sidecars" seam + charly/sidecar.go's embeddedSidecarBodies are ALSO DELETED
// (K-wave 2 cone R3): the embedded sidecar library is THIS plugin's own go:embed
// (sidecar_embedded.go), so the resolve has no host-resident input left. The plugin
// InvokeProviders kind:sidecar directly (RDD-proven live — plugin-build/fleet/check already
// InvokeProvider kind:*), adapts the reply, and provisions sidecar secrets ITSELF via
// deploykit.ProvisionPodmanSecrets + the SHARED deploykit.CredentialAccessViaExecutor (the SAME
// credential drive enc_tunnel_resolve.go / lifecycle.go use — no core round-trip).

// credServiceVNC mirrors charly/credential_plugin.go's CredServiceVNC (the VNC credential service
// name deploykit.ProvisionPodmanSecrets keys the auto-generated VNC password under). A plain stable
// string, passed as the credServiceVNC arg — no shared cross-module const exists (the core one is
// package main).
const credServiceVNC = "charly/vnc"

// sidecarResolveResult is the plugin-side shape of the former spec.PodConfigResolveSidecarsReply.
type sidecarResolveResult struct {
	Sidecars         []deploykit.ResolvedSidecar
	AppEnv           []string
	ExtraEnv         []string
	PersistOverrides map[string]json.RawMessage
}

// resolvePodSidecars resolves a deploy's sidecars end-to-end plugin-side. deploySidecars is the
// deploy's own `sidecar:` override map; projectTemplates is the project-root sidecar templates
// (deploykit.SidecarTemplatesOf(dc)). Returns (empty result, nil) when the deploy declares no
// sidecars.
func resolvePodSidecars(ctx context.Context, ex *sdk.Executor, deploySidecars, projectTemplates map[string]json.RawMessage, cliEnv []string, box, instance, runEngine string, autoGen bool, refreshSecret []string) (sidecarResolveResult, error) {
	if len(deploySidecars) == 0 {
		return sidecarResolveResult{AppEnv: cliEnv}, nil
	}
	// The embedded sidecar-template library is THIS plugin's own go:embed now (the
	// "pod-config-list-sidecars" seam + charly/sidecar.go's embeddedSidecarBodies are DELETED,
	// K-wave 2 cone R3) — the tailscale default lives here, byte-identical to the former
	// charly-binary-resident bodies.
	embedded, err := embeddedSidecarLibrary()
	if err != nil {
		return sidecarResolveResult{}, fmt.Errorf("reading embedded sidecar library: %w", err)
	}

	// Resolve via kind:sidecar's OpResolve (the single point sidecar defs are resolved — the host
	// reads no spec.Sidecar fields; the plugin owns all sidecar business logic).
	inJSON, err := json.Marshal(spec.SidecarResolveInput{
		EmbeddedTemplates: embedded,
		ProjectTemplates:  projectTemplates,
		DeployOverrides:   deploySidecars,
		CliEnv:            cliEnv,
		Box:               box,
		Instance:          instance,
	})
	if err != nil {
		return sidecarResolveResult{}, err
	}
	resJSON, err := ex.InvokeProvider(ctx, "kind", "sidecar", sdk.OpResolve, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return sidecarResolveResult{}, fmt.Errorf("sidecar resolve: %w", err)
	}
	var reply spec.SidecarResolveReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return sidecarResolveResult{}, err
		}
	}

	// Adapt + provision sidecar secrets (best-effort — mirrors the former in-seam Warning-only
	// handling: a provisioning error skips that sidecar's secret, never fails the deploy).
	resolved := make([]deploykit.ResolvedSidecar, 0, len(reply.Sidecars))
	for _, rs := range reply.Sidecars {
		resolved = append(resolved, resolvedSidecarFromSpec(rs))
	}
	var extraEnv []string
	cred := deploykit.CredentialAccessViaExecutor(ctx, ex)
	for i, sc := range resolved {
		if len(sc.Secret) == 0 {
			continue
		}
		scSecrets, _ := deploykit.ApplySecretRefresh(sc.Secret, refreshSecret)
		scProvisioned, scFallback, scErr := deploykit.ProvisionPodmanSecrets(runEngine, box, instance, scSecrets, autoGen, credServiceVNC, cred)
		if scErr != nil {
			continue
		}
		resolved[i].Secret = scProvisioned
		extraEnv = append(extraEnv, scFallback...)
	}

	return sidecarResolveResult{
		Sidecars:         resolved,
		AppEnv:           reply.AppEnv,
		ExtraEnv:         extraEnv,
		PersistOverrides: reply.PersistOverrides,
	}, nil
}

// resolvedSidecarFromSpec adapts one plugin-resolved spec.ResolvedSidecar into the deploykit
// quadlet-gen ResolvedSidecar shape. Relocated verbatim from charly/sidecar.go (seam-death).
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
