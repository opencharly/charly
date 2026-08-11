package fleet

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/spec"
)

// egress.go — the egress-validation dispatch, relocated from the deleted charly/service_render.go
// (K-wave 2 cone R2). The validation logic + CUE schemas live in the compiled-in candy/plugin-egress;
// these functions resolve verb:egress and Invoke its ops.OpValidate. The egress gate proves the
// config artifacts charly WRITES (cloud-init, kubernetes manifests, traefik routes, ledger JSON, the
// Containerfile, systemd/supervisord units, libvirt domain XML) BEFORE the bytes hit disk.
//
// This package is the SOLE ledger writer for every substrate (deploy_target.go's recordDeploy/
// recordVenueLedger), so the init() binding below wires the egress validator into sdk/kit's
// swappable spec.ValidateRecord seam exactly where the ledger writes happen — the same-process
// compiled-in placement that makes the seam always correctly wired (the former core init() in
// service_render.go did the same, from the other side of the boundary). The plugin-vm
// vm_egress_shim.go precedent is the same InvokeProvider(verb,"egress") reach, one package over.

// init binds the egress validator into the ledger's record-write seam (spec.ValidateRecord,
// called by sdk/kit's install_ledger.go on every ledger write).
func init() { spec.ValidateRecord = ValidateEgressValue }

// egressValidate Invokes verb:egress OpValidate with the {kind,label,mode,data} artifact; a
// non-empty reply error means the artifact violates the egress schema. Best-effort graceful-degrade
// with no reverse channel (a non-command context skips validation — matches the moved VM CLI's
// vm_egress_shim.go contract); a reachable-but-failing provider is LOUD (the write must not proceed
// unvalidated).
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
		return fmt.Errorf("%s: egress: %w", label, err)
	}
	var reply struct {
		Error string `json:"error"`
	}
	if len(out) > 0 {
		// A decode failure here must NOT be swallowed: reply.Error staying "" on malformed JSON would
		// silently treat a corrupted egress-validation reply as "validation passed" — precisely the
		// discarded-decode-errors class the moved VM shim's own comment closes, and load-bearing here
		// since egress validation is what catches a genuinely-broken rendered artifact BEFORE it
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

// ValidateEgress validates already-serialized YAML or JSON bytes against the egress kind's
// schema before they are written. JSON is a YAML subset, so one ingest path covers both.
func ValidateEgress(kind, label string, data []byte) error {
	return egressValidate(kind, label, "bytes", string(data))
}

// ValidateEgressValue validates an in-memory Go value (a manifest map[string]any, a record
// struct) by marshalling it to JSON and validating as bytes — faithful for the data values
// egress gates (kubernetes manifests, ledger records).
func ValidateEgressValue(kind, label string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%s: egress marshal value: %w", label, err)
	}
	return egressValidate(kind, label, "bytes", string(data))
}
