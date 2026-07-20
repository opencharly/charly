package main

import (
	"context"
	"encoding/json"

	"github.com/opencharly/sdk/spec"
)

// vm_plugin_client.go is the HOST→plugin client for the internal VM-resolution ops. The go-libvirt
// impl moved OUT of charly's core into the out-of-process candy/plugin-vm; the spice/vnc/ssh/status/
// preempt consumers that used to connectLibvirt / ResolveVmTarget directly now call invokeVmPlugin,
// which RPCs the vm plugin (the verb:libvirt provider) and decodes the structured result. Graceful
// degrade (ok=false) when the plugin is absent — the dependent core path then falls back / no-ops,
// rather than failing to compile (the plan's "core reaches the plugin through the registry").
//
// Cutover B unit 2 (R-E4): the wire shapes (spec.VmPluginEnv / spec.VmSnapInternalReq /
// spec.VmDisplayEndpoint / spec.VmResolveResult) are now CUE-sourced (sdk/schema/vmclient.cue) —
// they were hand-written Go structs here (an SDD violation) mirrored by an INDEPENDENT
// hand-written twin in candy/plugin-vm (vm_target.go's DisplayEndpoint decoded the identical
// shape by field name). Both retype onto the SAME generated defs now (R3, one shape). This
// dispatch function itself STAYS — connectPluginByWordRef + Operation are core-only (the provider
// registry, a kernel Mechanism), so the true "shared client" (plugin-deploy-vm calling plugin-vm's
// libvirt primitives peer-to-peer) is a SEPARATE FINAL/K5 IOU gated on the InvokeProvider
// generalization, not this move.

// invokeVmPlugin RPCs the out-of-process vm plugin for an internal VM-resolution op
// (domain-state / list-domains / resolve-spice / resolve-vnc) and returns the decoded JSON
// result. ok=false when the plugin is absent (graceful degrade) or the call errored.
func invokeVmPlugin(vmOp, vmName, uri string) (json.RawMessage, bool) {
	return invokeVmPluginEnv(spec.VmPluginEnv{VmOp: vmOp, VmName: vmName, URI: uri})
}

// vmPluginOpError decodes the `error` field from a lifecycle op reply ("" = success).
func vmPluginOpError(raw json.RawMessage) string {
	var r struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.Error
}

// vmPluginOpFlag reports whether the plugin's reply carries a true boolean under key.
// The idempotent lifecycle ops report WHAT they actually did — "already_running" from
// start, "already_gone" from destroy — so the CLI can say so instead of claiming an
// action it never took.
func vmPluginOpFlag(raw json.RawMessage, key string) bool {
	var r map[string]any
	if err := json.Unmarshal(raw, &r); err != nil {
		return false
	}
	b, _ := r[key].(bool)
	return b
}

// vmPluginCandyRef is the canonical @github ref to the external VM plugin candy (candy/plugin-vm,
// the verb:libvirt provider). Core RPCs verb:libvirt directly + unconditionally, but the plugin
// candy is external (not in compiled_plugins, not in any box's image closure), so the VM-RPC load
// paths — the invokeVmPluginEnv out-call here (via connectPluginByWordRef) + the check runner
// (resolveCheckRunnerContext) — must pull it in via ResolveOpts.ExtraCandyRefs (its documented purpose: a host-side plugin candy
// outside the image closure). In a check bed CHARLY_REPO_OVERRIDE redirects it to the local
// superproject under development; outside a bed it fetches the published candy.
func vmPluginCandyRef() string {
	return "@" + spec.DefaultProjectRepo + "/candy/plugin-vm"
}

// invokeVmPluginEnv is the full-env variant (lifecycle ops carry Force/DeleteDisk).
func invokeVmPluginEnv(env spec.VmPluginEnv) (json.RawMessage, bool) {
	prov, ok := connectPluginByWordRef(ClassVerb, "libvirt", vmPluginCandyRef())
	if !ok {
		return nil, false
	}
	envJSON, err := marshalJSON(env)
	if err != nil {
		return nil, false
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: "libvirt", Op: OpRun, Env: envJSON})
	if err != nil || out == nil {
		return nil, false
	}
	return out.JSON, true
}
