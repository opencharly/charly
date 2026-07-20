package main

import (
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// pod_lifecycle_resolve.go — the residual HOST-side bodies behind the "pod-config-enc-ensure-plan"
// / "pod-config-enc-unmount-plan" / "pod-config-container-tunnel" / "pod-config-box-engine" seams
// (host_build_pod_config_seams.go). P13-KERNEL step-4(ii): the REST of this file's former
// resolvers (resolvePodStartPlan/resolvePodStartQuadlet/resolvePodStartDirect/resolvePodStopPlan/
// resolvePodRuntimeImage/resolvePodAttachPlan/resolvePodShellPlan/resolvePodCmdPlan/
// resolvePodLogsPlan + the argv builders) moved to candy/plugin-deploy-pod (the plugin now
// SELF-RESOLVES its own start/stop/attach/logs plans — see lifecycle.go — instead of the host
// pre-resolving + threading a plan JSON). What stays: the three genuinely credential/registry-
// coupled leaves (encPlanFor+resolveEncPassphrase — the ONE narrow credential seam per the
// standing ruling; containerImage+ExtractMetadata+MergeDeployOntoMetadata+TunnelConfigFromMetadata
// — reads a RUNNING container's baked image, registry/podman-store coupled) + the box-engine
// per-host-config lookup (loader-coupled).

// resolvePodEncEnsure builds the pre-built spec.EncExecInput (ensure) the plugin InvokeProviders
// verb:enc with, or (nil, nil) when no encrypted volume is configured OR every one is already
// mounted (the keyring-resilient fast path preserved from ensureEncryptedMounts). resolveEncPassphrase
// + encPlanFor stay HOST-side (credential store + config reads a plugin cannot do); a passphrase
// resolution failure fails the start, exactly as the former in-core ensureEncryptedMounts did.
func resolvePodEncEnsure(box, instance string) (spec.RawBody, error) {
	plan, err := encPlanFor(box, instance, "", box)
	if err != nil || len(plan) == 0 {
		return nil, nil // no encrypted mounts configured (load error swallowed, as before)
	}
	anyNotReady := false
	for _, m := range plan {
		if !m.Initialized || !m.Mounted {
			anyNotReady = true
			break
		}
	}
	if !anyNotReady {
		return nil, nil // all mounted — skip the passphrase lookup + the enc leg
	}
	passphrase, err := resolveEncPassphrase(box, false)
	if err != nil {
		return nil, fmt.Errorf("resolving enc passphrase for %s: %w", box, err)
	}
	body, err := marshalJSON(spec.EncExecInput{
		Method:     spec.EncMethodEnsure,
		ImageID:    "charly-" + box,
		BoxName:    box,
		Passphrase: passphrase,
		Volumes:    plan,
	})
	return body, err
}

// resolvePodEncUnmount builds the spec.EncExecInput (unmount) the plugin InvokeProviders verb:enc
// with on `charly stop --unmount`, or nil when no encrypted volume is configured. Mirrors encUnmount.
func resolvePodEncUnmount(box, instance string) (spec.RawBody, error) {
	plan, err := encPlanFor(box, instance, "", deploykit.DeployStorageDir(box, instance))
	if err != nil || len(plan) == 0 {
		return nil, nil
	}
	body, err := marshalJSON(spec.EncExecInput{
		Method:  spec.EncMethodUnmount,
		ImageID: "charly-" + box,
		BoxName: box,
		Volumes: plan,
	})
	return body, err
}

// resolvePodTunnel resolves the tunnel config (charly.yml-only; labels never carry tunnel) the
// plugin starts/stops, or nil when none is configured. Reads the RUNNING container's baked image
// ref (registry/podman-store coupled — genuinely host-only).
func resolvePodTunnel(box, instance string) *spec.TunnelConfig {
	dc := deploykit.LoadDeployConfigForRead("charly start tunnel")
	ctrName := kit.ContainerNameInstance(box, instance)
	imageRef := containerImage("podman", ctrName)
	if imageRef == "" {
		return nil
	}
	meta, err := ExtractMetadata("podman", imageRef)
	if err != nil || meta == nil {
		return nil
	}
	deploykit.MergeDeployOntoMetadata(meta, dc, box, instance)
	if meta.Tunnel == nil {
		return nil
	}
	return TunnelConfigFromMetadata(meta)
}
