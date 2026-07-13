package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// oci_plugin.go is the CORE adapter for the externalized OCI IMAGE ENGINE (the P14a
// cutover). The go-containerregistry layer-MERGE engine (formerly charly/merge.go) + the
// remote-image adopt-user PROBE (formerly charly/registry.go) live
// OUT-OF-PROCESS in candy/plugin-oci (verb:oci); charly/go.mod links go-containerregistry
// NOWHERE. The core consumers reach the plugin through the provider registry:
//
//   - `charly box merge` (merge.go MergeCmd.runOne) resolves a box → spec.MergeRequest and
//     forwards it via invokeOciMerge, printing the reply's progress Notes;
//   - the build engine's adopt-user resolution (generate.go) calls invokeOciInspectUser;
//   - candy/plugin-build's build DRIVE calls Executor.InvokeProvider(verb:oci) directly
//     (the F10 peer-dispatch leg — plugin↔plugin, not this core shim).
//
// MIGRATION INVENTORY (north-star §4.4 — every stays-core construct has a named K-wave exit):
// this oci_plugin.go core shim is UNTIL-K3. generate.go's adopt-user consumer (invokeOciInspectUser)
// + the `charly box merge` CLI consumer move WITH the build engine into buildkit/plugin-build at K3;
// this host→verb:oci adapter dies or shrinks to the build-engine's own InvokeProvider(verb:oci) then.
//
// verb:oci is a pure INTERNAL RPC keyed by an oci_op ENV discriminator (mirroring the vm
// plugin's VmOp — the request struct rides Params, the leg selector rides Env), NOT a
// `plugin_input`-enveloped check verb. It is COMPILED INTO charly by default
// (compiled_plugins:), so providerRegistry resolves it in-process AND project-lessly (the
// merge + adopt-user probes run on the build path, on hosts that may carry no project);
// the connectPluginByWord fallback covers the baked / project-source coexist paths — the
// registry-first pattern of tunnel_plugin.go / credential_plugin.go.

// ociEnvMerge / ociEnvInspectUser are the env-JSON selectors matching candy/plugin-oci's
// ociEnv{OciOp}.
const (
	ociOpMerge       = "merge"
	ociOpInspectUser = "inspect-user"
)

// ociProvider resolves verb:oci. Registry-first so a COMPILED-IN plugin resolves in-process
// and project-lessly; falls back to connectPluginByWord for the baked / project-source
// coexist paths.
func ociProvider() (Provider, bool) {
	if p, ok := providerRegistry.resolve(ClassVerb, "oci"); ok {
		return p, true
	}
	return connectPluginByWord(ClassVerb, "oci")
}

// invokeOci dispatches one internal oci op (params = the request struct JSON, env = the
// oci_op selector) and returns the raw reply JSON.
func invokeOci(ociOp string, params any) (json.RawMessage, error) {
	prov, ok := ociProvider()
	if !ok {
		return nil, fmt.Errorf(
			"oci plugin (verb:oci) did not connect — candy/plugin-oci is compiled into charly " +
				"(compiled_plugins) by default; on a custom build install it alongside charly " +
				"(/usr/lib/charly/plugins) or run from a project composing it")
	}
	paramsJSON, err := marshalJSON(params)
	if err != nil {
		return nil, err
	}
	envJSON, err := marshalJSON(map[string]string{"oci_op": ociOp})
	if err != nil {
		return nil, err
	}
	out, err := prov.Invoke(context.Background(), &Operation{
		Reserved: "oci",
		Op:       OpRun,
		Params:   paramsJSON,
		Env:      envJSON,
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("oci: verb:oci returned no result")
	}
	return out.JSON, nil
}

// invokeOciMerge forwards a resolved merge request to verb:oci and decodes the
// spec.MergeReply (layer counts + progress Notes + a per-merge Error).
func invokeOciMerge(req spec.MergeRequest) (spec.MergeReply, error) {
	raw, err := invokeOci(ociOpMerge, req)
	if err != nil {
		return spec.MergeReply{}, err
	}
	var reply spec.MergeReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return spec.MergeReply{}, fmt.Errorf("oci merge: decode reply: %w", err)
	}
	return reply, nil
}

// invokeOciInspectUser probes a remote image's /etc/passwd for the user at uid via
// verb:oci, returning the spec.UserInfo (Found=false when no such user / the image can't
// be inspected — the former nil-return convention).
func invokeOciInspectUser(ref string, uid int) (spec.UserInfo, error) {
	raw, err := invokeOci(ociOpInspectUser, spec.ImageUserInput{Ref: ref, UID: uid})
	if err != nil {
		return spec.UserInfo{}, err
	}
	var info spec.UserInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return spec.UserInfo{}, fmt.Errorf("oci inspect-user: decode reply: %w", err)
	}
	return info, nil
}
