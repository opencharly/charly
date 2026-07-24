package deploypod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
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
// the wave γ DeployStateHost fix) given a dc it ALREADY holds (or loads once via the EXISTING
// "pod-config-load-bundle" seam, loadDeploy — never the bare deploykit.LoadBundleConfig()/
// LoadDeployConfigForRead() a plugin cannot safely reach, per the DeployStateHost placement-
// dependency class). The credential touch (enc-ensure's passphrase resolution) dispatches
// verb:credential directly via InvokeProvider — this package's own small wire copy, the SAME
// established convention candy/plugin-pod/enc_cmd.go and candy/plugin-settings/config.go already
// each keep (no shared exported fixture across modules).

// credentialInput/credentialReply are the verb:credential wire forms — byte-compatible with the
// other packages' copies (charly/credential_plugin.go, candy/plugin-pod/enc_cmd.go,
// candy/plugin-settings/config.go).
type credentialInput struct {
	Method  string `json:"method"`
	Service string `json:"service,omitempty"`
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
}

type credentialReply struct {
	Value  string `json:"value,omitempty"`
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

// credentialResolve resolves one credential via verb:credential InvokeProvider — the
// deploykit.CredentialAccess.Resolve half resolvePodEncEnsurePlan's passphrase resolution needs.
// A nil exec or a transport error degrades to ("", "unavailable"), matching
// charly/credential_plugin.go's pluginCredentialStore.resolve error-handling shape exactly, so
// deploykit.ResolveEncPassphrase's fallback chain (env → store → generate/prompt) behaves
// identically regardless of which package's CredentialAccess backs it.
func credentialResolve(ctx context.Context, ex *sdk.Executor, service, key, defaultVal string) (value, source string) {
	if ex == nil {
		return defaultVal, "unavailable"
	}
	inJSON, err := json.Marshal(credentialInput{Method: "resolve", Service: service, Key: key})
	if err != nil {
		return defaultVal, "unavailable"
	}
	resJSON, err := ex.InvokeProvider(ctx, "verb", "credential", sdk.OpRun, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return defaultVal, "unavailable"
	}
	var reply credentialReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return defaultVal, "unavailable"
		}
	}
	if reply.Value != "" {
		return reply.Value, reply.Source
	}
	src := reply.Source
	if src == "" {
		src = "default"
	}
	return defaultVal, src
}

// credentialWrite persists one credential via verb:credential InvokeProvider — the
// deploykit.CredentialAccess.Write half (the auto-generate-on-first-ensure path).
func credentialWrite(ctx context.Context, ex *sdk.Executor, service, key, value string) error {
	if ex == nil {
		return fmt.Errorf("plugin-deploy-pod credential write: no host reverse channel")
	}
	inJSON, err := json.Marshal(credentialInput{Method: "set", Service: service, Key: key, Value: value})
	if err != nil {
		return err
	}
	resJSON, err := ex.InvokeProvider(ctx, "verb", "credential", sdk.OpRun, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	var reply credentialReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return uerr
		}
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	return nil
}

// pluginCredentialAccess bundles the two RPC-backed operations into the shape
// deploykit.ResolveEncPassphrase needs — the plugin-side analogue of
// charly/enc.go's former coreCredentialAccess() / candy/plugin-pod/enc_cmd.go's
// pluginCredentialAccess().
func pluginCredentialAccess(ctx context.Context, ex *sdk.Executor) deploykit.CredentialAccess {
	return deploykit.CredentialAccess{
		Resolve: func(envVar, service, key, defaultVal string) (string, string) {
			return credentialResolve(ctx, ex, service, key, defaultVal)
		},
		Write: func(service, key, value string) error {
			return credentialWrite(ctx, ex, service, key, value)
		},
	}
}

// resolvePodEncEnsurePlan builds the pre-built spec.EncExecInput (ensure) the caller
// InvokeProviders verb:enc with, or (nil, nil) when no encrypted volume is configured OR every
// one is already mounted (the keyring-resilient fast path — direct port of
// charly/pod_lifecycle_resolve.go's resolvePodEncEnsure). dc is loaded ONCE by the caller (either
// reused from an already-loaded podRuntimeImage.dc, or freshly loaded via loadDeploy — the
// EXISTING "pod-config-load-bundle" seam) — never re-derived from a bare
// deploykit.LoadBundleConfig() call, which silently degrades outside charly-core (the
// DeployStateHost placement-dependency class, sdk#84ee126's EncPlanForConfig exists precisely to
// avoid it here).
func resolvePodEncEnsurePlan(ctx context.Context, ex *sdk.Executor, dc *deploykit.BundleConfig, box, instance string) (spec.RawBody, error) {
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
	passphrase, err := deploykit.ResolveEncPassphrase(box, false, pluginCredentialAccess(ctx, ex))
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
func resolvePodEncUnmountPlan(dc *deploykit.BundleConfig, box, instance string) (spec.RawBody, error) {
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
func resolvePodTunnelPlan(dc *deploykit.BundleConfig, box, instance string) *spec.TunnelConfig {
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
