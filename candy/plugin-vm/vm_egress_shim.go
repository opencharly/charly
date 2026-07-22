package vm

import (
	"encoding/json"
	"errors"

	"github.com/opencharly/sdk"
)

// vm_egress_shim.go — the moved VM CLI's egress reach. The egress SUBSYSTEM (vendored CUE schemas)
// lives in candy/plugin-egress (verb:egress, OpValidate); the plugin validates its rendered libvirt
// domain XML + cloud-init seed by Invoking it over the reverse channel, exactly as core's
// ValidateEgress* functions are thin shims to the SAME provider. This REPLACES the former host-side
// two-phase validation (the plugin is compiled-in now, so it reaches verb:egress in-proc) — the
// design that egress_stub.go's no-op stood in for is superseded here.

// egressValidate Invokes verb:egress OpValidate with the {kind,label,mode,data} artifact; a non-empty
// reply error means the artifact violates the egress schema. Best-effort graceful-degrade with no
// reverse channel (matches core's never-crash contract — a non-command context skips validation).
func egressValidate(kind, label, mode, data string) error {
	if cmdExec == nil {
		return nil
	}
	params, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
		Mode  string `json:"mode"`
		Data  string `json:"data"`
	}{Kind: kind, Label: label, Mode: mode, Data: data})
	if err != nil {
		return err
	}
	out, err := cmdExec.InvokeProvider(cmdCtx, "verb", "egress", sdk.OpValidate, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	var reply struct {
		Error string `json:"error"`
	}
	if len(out) > 0 {
		// A decode failure here must NOT be swallowed: reply.Error staying "" on malformed JSON would
		// silently treat a corrupted egress-validation reply as "validation passed" — precisely the
		// discarded-decode-errors class this fix closes, and load-bearing here since egress validation
		// is what catches a genuinely-broken rendered libvirt domain XML / cloud-init seed BEFORE it
		// reaches disk.
		if err := json.Unmarshal(out, &reply); err != nil {
			return errors.New("decode egress validate reply: " + err.Error())
		}
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	return nil
}

// ValidateXMLEgress validates a rendered libvirt domain XML against the egress schema (verb:egress).
func ValidateXMLEgress(kind, label, xmlStr string) error {
	return egressValidate(kind, label, "xml", xmlStr)
}
