package deploypod

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// enc_tunnel_resolve.go — the pod START/STOP plan resolvers' enc-ensure/enc-unmount/tunnel legs,
// relocated from charly/pod_lifecycle_resolve.go (wave γ, extending the ALREADY-LIVE InvokeProvider
// pattern lifecycle.go proves for the enc/tunnel EXECUTION legs — resolve.go's callers already do
// `exec.InvokeProvider(ctx, "verb", "enc"/"tunnel", …)` with the plan THIS file now builds locally
// instead of fetching it from three narrow core seams).
//
// The former "pod-config-enc-ensure-plan" / "pod-config-enc-unmount-plan" /
// "pod-config-container-tunnel" HostBuild seams are RETIRED here — every caller in resolve.go now
// builds its own plan via deploykit.EncPlanForConfig/EncPlanForConfig's sibling functions (sdk#84ee126,
// the wave γ DeployStateHost fix) given a dc it ALREADY holds (or loads once via loadDeploy, the
// cycle-free loaderkit.LoadHostFleetConfigViaExecutor read — never the bare deploykit.LoadFleetConfig()/
// LoadDeployConfigForRead() a plugin cannot safely reach, per the DeployStateHost placement-
// dependency class). The credential touch (enc-ensure's passphrase resolution) dispatches
// verb:credential via the SHARED deploykit.CredentialAccessViaExecutor helper (R3 — the ONE
// verb:credential-backed CredentialAccess every deploy-time plugin uses; #55 K4 extracted it from
// this package's former local pluginCredentialAccess copy). The 3 other pre-existing per-candy
// copies migrate onto it via task #86.

// resolvePodEncEnsurePlan builds the pre-built spec.EncExecInput (ensure) the caller
// InvokeProviders verb:enc with, or (nil, nil) when no encrypted volume is configured OR every
// one is already mounted (the keyring-resilient fast path — direct port of
// charly/pod_lifecycle_resolve.go's resolvePodEncEnsure). dc is loaded ONCE by the caller (either
// reused from an already-loaded podRuntimeImage.dc, or freshly loaded via loadDeploy, the
// cycle-free loaderkit.LoadHostFleetConfigViaExecutor read) — never re-derived from a bare
// deploykit.LoadFleetConfig() call, which silently degrades outside charly-core (the
// DeployStateHost placement-dependency class, sdk#84ee126's EncPlanForConfig exists precisely to
// avoid it here).
func resolvePodEncEnsurePlan(ctx context.Context, ex *sdk.Executor, dc *deploykit.FleetConfig, box, instance string, autoGenerate bool) (spec.RawBody, error) {
	plan, err := deploykit.EncPlanForConfig(dc, box, instance, "", box)
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
		return nil, nil
	}
	passphrase, err := deploykit.ResolveEncPassphrase(box, autoGenerate, deploykit.CredentialAccessViaExecutor(ctx, ex))
	if err != nil {
		return nil, fmt.Errorf("resolving enc passphrase for %s: %w", box, err)
	}
	return json.Marshal(spec.EncExecInput{
		Method:     spec.EncMethodEnsure,
		ImageID:    "charly-" + box,
		BoxName:    box,
		Passphrase: passphrase,
		Volumes:    plan,
	})
}

// resolvePodEncUnmountPlan builds the spec.EncExecInput (unmount) the caller InvokeProviders
// verb:enc with on `charly stop --unmount`, or nil when no encrypted volume is configured. Direct
// port of charly/pod_lifecycle_resolve.go's resolvePodEncUnmount — no credential touch needed.
func resolvePodEncUnmountPlan(dc *deploykit.FleetConfig, box, instance string) (spec.RawBody, error) {
	plan, err := deploykit.EncPlanForConfig(dc, box, instance, "", deploykit.DeployStorageDir(box, instance))
	if err != nil || len(plan) == 0 {
		return nil, nil
	}
	return json.Marshal(spec.EncExecInput{
		Method:  spec.EncMethodUnmount,
		ImageID: "charly-" + box,
		BoxName: box,
		Volumes: plan,
	})
}

// resolvePodTunnelPlan resolves the tunnel config (charly.yml-only; labels never carry tunnel) the
// caller starts/stops, or nil when none is configured. Reads the RUNNING container's baked image
// ref (registry/podman-store coupled — genuinely host-only, but the plugin already drives podman
// directly elsewhere in this package). Direct port of charly/pod_lifecycle_resolve.go's
// resolvePodTunnel; dc is the SAME already-loaded config resolvePodEncEnsurePlan/UnmountPlan use
// (no redundant load).
func resolvePodTunnelPlan(dc *deploykit.FleetConfig, box, instance string) *spec.TunnelConfig {
	ctrName := kit.ContainerNameInstance(box, instance)
	imageRef := kit.ContainerImage("podman", ctrName)
	if imageRef == "" {
		return nil
	}
	meta, err := deploykit.ExtractMetadata("podman", imageRef)
	if err != nil || meta == nil {
		return nil
	}
	if dc != nil {
		deploykit.MergeDeployOntoMetadata(meta, dc, box, instance)
	}
	if meta.Tunnel == nil {
		return nil
	}
	return deploykit.TunnelConfigFromMetadata(meta)
}
