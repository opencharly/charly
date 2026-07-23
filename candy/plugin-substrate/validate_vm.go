package substratekind

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// validate_vm.go — the "vm" capability's F7/C8 deep OpValidate check (ProvidedCapability.
// Validates=true, set ONLY on the "vm" entry in NewMeta — pod/k8s/local/android declare no
// deep check and pay no extra round-trip). This is where the vm PCI-hostdev concreteness fix
// belongs, per the kernel/plugin boundary law: the host's closedness-only value gate
// (charly/provider_kind_invoke.go's validateKindValueCUE) legitimately stays document-wide
// and non-concrete forever — a kernel branch encoding "which fields a type:pci hostdev needs"
// would be exactly the R-item the boundary law forbids leaking into core (see that function's
// comment for the full history, including a first attempt that DID land it in the kernel and
// was overruled). The check itself lives HERE instead.
//
// The gap: sdk/schema/vm.cue's #LibvirtHostdev requires domain/bus/slot/function
// CONDITIONALLY (`if type == "pci"`), but only their TYPE (#LibvirtPCIHex, a hex-string
// pattern) — never their CONCRETENESS. An entirely-omitted field unifies down to the bare
// type constraint, which the host's closedness-only CUE gate accepts (there is no concrete
// value to check the pattern against). So a PCI hostdev missing e.g. slot/function silently
// passes host-side validation.
//
// The fix needs no CUE concreteness machinery at all: `source` is an OPEN `map[string]string`
// (`source: {[string]: string}` in the CUE, `Source map[string]string` in the generated Go —
// spec/union_types.go's hand-written LibvirtHostdev), and a Go map decoded from JSON
// DISTINGUISHES "key omitted" (`ok == false`) from "key present with an empty value"
// (`ok == true, v == ""`) — exactly the distinction closedness-only CUE validation loses. So
// this check just verifies presence + non-emptiness of the 4 PCI fields, on the RAW authored
// body the host threads via op.Params (the same body the flat op.Params kind path validates
// against) — no re-decode into a canonical spec.Vm needed.
type vmValidateBody struct {
	Libvirt struct {
		Devices struct {
			Hostdevs []struct {
				Type   string            `json:"type"`
				Source map[string]string `json:"source"`
			} `json:"hostdevs"`
		} `json:"devices"`
	} `json:"libvirt"`
}

// pciHostdevRequiredFields is the exact 4-field set sdk/schema/vm.cue's #LibvirtHostdev
// requires (conditionally) for a `type: pci` hostdev — kept in lockstep with that def.
var pciHostdevRequiredFields = [...]string{"domain", "bus", "slot", "function"}

// validateVmDeep runs the vm kind's F7/C8 deep OpValidate check against the raw authored
// entity body. No-op (empty Diagnostics) for: a vm with no `libvirt.devices.hostdevs`
// authored at all (the corpus-wide norm — GPU-passthrough hostdevs are auto-detected and
// injected into the per-host instance.yml overlay at `charly vm create` time, never authored
// directly in a project's charly.yml); and any non-`type: pci` hostdev (only a PCI hostdev has
// conditionally-required source sub-fields per the schema).
func validateVmDeep(paramsJSON json.RawMessage) (spec.Diagnostics, error) {
	var body vmValidateBody
	if len(paramsJSON) > 0 {
		if err := json.Unmarshal(paramsJSON, &body); err != nil {
			return spec.Diagnostics{}, fmt.Errorf("plugin-substrate: vm OpValidate: decode entity: %w", err)
		}
	}
	var diags spec.Diagnostics
	for i, hd := range body.Libvirt.Devices.Hostdevs {
		if hd.Type != "pci" {
			continue
		}
		for _, field := range pciHostdevRequiredFields {
			if v, ok := hd.Source[field]; !ok || v == "" {
				diags.Items = append(diags.Items, spec.Diagnostic{
					Severity: "error",
					Path:     fmt.Sprintf("libvirt.devices.hostdevs[%d].source.%s", i, field),
					Message:  "must be a concrete PCI hex address (required for a type: pci hostdev)",
				})
			}
		}
	}
	return diags, nil
}
