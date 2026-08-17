package substratekind

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/spec/spec"
)

// validate_vm.go — the "vm" capability's F7/C8 deep OpValidate check (ProvidedCapability.
// Validates=true, set ONLY on the "vm" entry in NewMeta — pod/kubernetes/local/android declare no
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
	Source struct {
		Kind   string `json:"kind"`
		Distro string `json:"distro"`
	} `json:"source"`
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
	diags.Items = append(diags.Items, validateSourceDistro(body.Source.Kind, body.Source.Distro)...)
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

// distroBearingSourceKinds are the VmSource arms whose `distro:` the renderers consume.
// A cloud_image's distro selects the guest package NAME, package MANAGER and sshd UNIT
// name; a bootstrap's keys the embedded build vocabulary and the guest package manager.
// The other arms (disk / from_vm / from_snapshot / bootc) carry no distro at all.
var distroBearingSourceKinds = map[string]bool{"cloud_image": true, "bootstrap": true}

// validateSourceDistro enforces that a distro-bearing VM source declares `distro:`.
//
// The CUE schema types it `distro?: #DistroID` — OPTIONAL, and closed to the generated
// vocabulary. Closedness does real work: a misspelled value is a unification conflict, so
// `distro: redhat` fails validation. Presence does not: the host's value gate is
// closedness-only by design (charly/provider_kind_invoke.go documents why that is
// permanent), and vm.cue's own comment records why a `!` marker or a non-optional field
// would not help either — the former is not reported by that gate, the latter only turns an
// absent value into a non-concrete DECODE error in applyCueDefaults. So PRESENCE is checked
// here, in the kind's own plugin, exactly as the PCI hostdev fields above are: the same "Go
// distinguishes absent from empty where non-concrete CUE cannot" shape.
//
// Why it is worth a check rather than a default: the renderers used to INFER the distro from
// `base_user` (arch/alpine only) and fall back to Arch/Fedora conventions for everything
// else, so a Debian-family image that omitted it was rendered `openssh` +
// `systemctl enable --now sshd` and booted with sshd masked and unreachable — a silent wrong
// render, days from the authoring mistake. The inference is deleted; this makes the omission
// it used to paper over a validation error instead.
func validateSourceDistro(kind, distro string) []spec.Diagnostic {
	if !distroBearingSourceKinds[kind] {
		return nil
	}
	if distro == "" {
		return []spec.Diagnostic{{
			Severity: "error",
			Path:     "source.distro",
			Message: fmt.Sprintf("must be declared for a %s source (one of %v) — it selects the guest's "+
				"package name, package manager and sshd unit name, and nothing else in the spec implies it",
				kind, spec.DistroIDs),
		}}
	}
	for _, id := range spec.DistroIDs {
		if distro == id {
			return nil
		}
	}
	return []spec.Diagnostic{{
		Severity: "error",
		Path:     "source.distro",
		Message:  fmt.Sprintf("%q is not a known distro id (one of %v)", distro, spec.DistroIDs),
	}}
}
