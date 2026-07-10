package main

import (
	"context"
	"encoding/json"
)

// vm_plugin_client.go is the HOST→plugin client for the internal VM-resolution ops. The go-libvirt
// impl moved OUT of charly's core into the out-of-process candy/plugin-vm; the spice/vnc/ssh/status/
// preempt consumers that used to connectLibvirt / ResolveVmTarget directly now call invokeVmPlugin,
// which RPCs the vm plugin (the verb:libvirt provider) and decodes the structured result. Graceful
// degrade (ok=false) when the plugin is absent — the dependent core path then falls back / no-ops,
// rather than failing to compile (the plan's "core reaches the plugin through the registry").

// vmPluginEnv is the host→plugin env for an internal VM-resolution RPC (matches candy/plugin-vm's
// vmEnv VmOp/VmName/URI json fields).
type vmPluginEnv struct {
	VmOp       string             `json:"vm_op"`
	VmName     string             `json:"vm_name"`
	URI        string             `json:"uri"`
	Force      bool               `json:"force,omitempty"`
	DeleteDisk bool               `json:"delete_disk,omitempty"`
	Create     *vmCreateReq       `json:"create,omitempty"`
	Snap       *vmSnapInternalReq `json:"snap,omitempty"`
	StateDir   string             `json:"state_dir,omitempty"`
}

// vmCreateReq is the host-resolved create payload sent to the plugin's "create" op (the host did
// the override/GPU/defaults/disk/seed/SSH/smbios/OVMF resolution; the plugin renders + creates).
type vmCreateReq struct {
	Spec         *VmSpec         `json:"spec"`
	RT           VmRuntimeParams `json:"rt"`
	VmDomainName string          `json:"vm_domain_name"`
	Home         string          `json:"home"`
	VmName       string          `json:"vm_name"`
	Name         string          `json:"name"`
	Backend      string          `json:"backend"`
	VmStateDir   string          `json:"vm_state_dir"`
	// ValidateOnly: the plugin renders the libvirt domain XML and RETURNS it
	// (rendered_domain_xml) WITHOUT creating, so the host can run the real
	// ValidateXMLEgress (the out-of-process plugin must not carry the egress
	// subsystem). The host then issues a second create call (ValidateOnly=false).
	// QEMU has no domain XML — the validate pass returns empty (cloud-init is
	// already egress-validated host-side via RegenerateSeedISO).
	ValidateOnly bool `json:"validate_only,omitempty"`
}

// invokeVmCreate RPCs the plugin's "create" op with the fully host-resolved request.
func invokeVmCreate(req vmCreateReq) (json.RawMessage, bool) {
	return invokeVmPluginEnv(vmPluginEnv{VmOp: "create", Create: &req})
}

// vmCreateRenderedXML decodes the libvirt domain XML the plugin returns from a
// ValidateOnly create pass ("" for the QEMU backend, which has no domain XML).
func vmCreateRenderedXML(raw json.RawMessage) string {
	var r struct {
		RenderedDomainXML string `json:"rendered_domain_xml"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.RenderedDomainXML
}

// displayEndpointWire decodes the vm plugin's resolve-spice/resolve-vnc result's `endpoint` (the
// plugin's DisplayEndpoint, marshaled by field name — no json tags; json.Unmarshal matches
// case-insensitively). The host builds the SPICE/VNC dialing + any ssh tunnel from it.
type displayEndpointWire struct {
	IsSocket     bool
	SocketPath   string
	Host         string
	Port         int
	Password     string
	TunnelNeeded bool
}

// vmResolveResult decodes a resolve-spice/resolve-vnc reply.
type vmResolveResult struct {
	Endpoint     displayEndpointWire `json:"endpoint"`
	Error        string              `json:"error"`
	TunnelTarget string              `json:"tunnel_target"`
}

// domainInfo decodes one entry from the plugin's list-domains reply (the plugin marshals its own
// domainInfo by field name; json.Unmarshal matches Name/State case-insensitively).
type domainInfo struct {
	Name  string
	State string
}

// invokeVmPlugin RPCs the out-of-process vm plugin for an internal VM-resolution op
// (domain-state / list-domains / resolve-spice / resolve-vnc) and returns the decoded JSON
// result. ok=false when the plugin is absent (graceful degrade) or the call errored.
func invokeVmPlugin(vmOp, vmName, uri string) (json.RawMessage, bool) {
	return invokeVmPluginEnv(vmPluginEnv{VmOp: vmOp, VmName: vmName, URI: uri})
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
// (attachCheckRunnerContext) — must pull it in via ResolveOpts.ExtraCandyRefs (its documented purpose: a host-side plugin candy
// outside the image closure). In a check bed CHARLY_REPO_OVERRIDE redirects it to the local
// superproject under development; outside a bed it fetches the published candy.
func vmPluginCandyRef() string {
	return "@" + DefaultProjectRepo + "/candy/plugin-vm"
}

// invokeVmPluginEnv is the full-env variant (lifecycle ops carry Force/DeleteDisk).
func invokeVmPluginEnv(env vmPluginEnv) (json.RawMessage, bool) {
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
