package vm

import (
	"encoding/json"
	"fmt"
)

// vm_engine_direct.go — the moved VM CLI handlers reach the libvirt/qemu ENGINE by a DIRECT in-package
// call now (command:vm and verb:libvirt are the SAME compiled-in plugin), not the former host→plugin
// RPC. These keep the handlers' call sites (invokeVmPlugin / invokeVmPluginEnv / invokeVmCreate +
// the reply decoders) byte-identical while dispatching straight to provider.go's dispatchInternalOp.

// vmPluginEnv aliases the plugin's own engine env so the handlers' vmPluginEnv{...} literals resolve
// in-package (the core RPC-client type stayed in core; the handlers no longer cross a process boundary).
type vmPluginEnv = vmEnv

// invokeVmPluginEnv runs an internal VM-resolution op against the in-package engine and returns the
// decoded JSON result (ok=false on a dispatch error — the handlers surface the concrete engine error).
func invokeVmPluginEnv(env vmEnv) (json.RawMessage, bool) {
	reply, err := dispatchInternalOp(env)
	if err != nil || reply == nil {
		return nil, false
	}
	return reply.GetResultJson(), true
}

// invokeVmPlugin is the lifecycle-op shorthand (domain-state / list-domains / start / …).
func invokeVmPlugin(vmOp, vmName, uri string) (json.RawMessage, bool) {
	return invokeVmPluginEnv(vmEnv{VmOp: vmOp, VmName: vmName, URI: uri})
}

// invokeVmCreate runs the "create" op with the fully host-resolved request (render + create).
func invokeVmCreate(req vmCreateReq) (json.RawMessage, bool) {
	return invokeVmPluginEnv(vmEnv{VmOp: "create", Create: &req})
}

// vmPluginOpError decodes the `error` field from an op reply ("" = success). A decode failure is
// itself surfaced as the returned error string — every call site treats "" as success, so silently
// swallowing a malformed/corrupted reply here (the discarded-decode-errors defect this fix closes)
// would have made every caller wrongly proceed as if a vm create/start/stop/… had SUCCEEDED.
func vmPluginOpError(raw json.RawMessage) string {
	var r struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Sprintf("decode vm plugin reply: %v", err)
	}
	return r.Error
}

// vmPluginOpFlag reports whether the reply carries a true boolean under key (already_running / already_gone).
func vmPluginOpFlag(raw json.RawMessage, key string) bool {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

// vmCreateRenderedXML decodes the libvirt domain XML a ValidateOnly create pass returns.
func vmCreateRenderedXML(raw json.RawMessage) string {
	var r struct {
		RenderedDomainXML string `json:"rendered_domain_xml"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.RenderedDomainXML
}
