package substratekind

import (
	"encoding/json"
	"testing"
)

// validate_vm_test.go — focused unit tests for validateVmDeep, the "vm" capability's F7/C8 deep
// OpValidate check (validate_vm.go). The end-to-end WIRING proof (the host actually dispatches
// OpValidate for "vm" through the real compiled-in registry and surfaces the plugin's
// Diagnostics as a load failure) lives in the companion charly-repo integration tests
// (charly/provider_kind_invoke_concrete_test.go) — these are the narrower, plugin-local unit
// tests for the check's own logic, mirroring the coverage the fix originally shipped with when
// it was (briefly, incorrectly) implemented host-side.

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return b
}

// TestValidateVmDeep_PCIHostdevMissingSlotFunction_Rejected: a type:pci hostdev whose source map
// omits slot/function produces two error diagnostics naming exactly those two fields.
func TestValidateVmDeep_PCIHostdevMissingSlotFunction_Rejected(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"libvirt": map[string]any{
			"devices": map[string]any{
				"hostdevs": []map[string]any{
					{
						"type":   "pci",
						"source": map[string]any{"domain": "0x0000", "bus": "0x01"},
					},
				},
			},
		},
	})
	diags, err := validateVmDeep(body)
	if err != nil {
		t.Fatalf("validateVmDeep: %v", err)
	}
	if !diags.HasErrors() {
		t.Fatal("expected error diagnostics for a PCI hostdev missing slot/function, got none")
	}
	if len(diags.Items) != 2 {
		t.Fatalf("expected exactly 2 diagnostics (slot + function), got %d: %+v", len(diags.Items), diags.Items)
	}
	gotPaths := map[string]bool{}
	for _, it := range diags.Items {
		if it.Severity != "error" {
			t.Errorf("expected severity=error, got %q", it.Severity)
		}
		gotPaths[it.Path] = true
	}
	for _, want := range []string{"libvirt.devices.hostdevs[0].source.slot", "libvirt.devices.hostdevs[0].source.function"} {
		if !gotPaths[want] {
			t.Errorf("missing expected diagnostic path %q; got %+v", want, diags.Items)
		}
	}
}

// TestValidateVmDeep_PCIHostdevComplete_NoDiagnostics proves a fully-specified PCI hostdev
// produces zero diagnostics.
func TestValidateVmDeep_PCIHostdevComplete_NoDiagnostics(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"libvirt": map[string]any{
			"devices": map[string]any{
				"hostdevs": []map[string]any{
					{
						"type":   "pci",
						"source": map[string]any{"domain": "0x0000", "bus": "0x01", "slot": "0x00", "function": "0x0"},
					},
				},
			},
		},
	})
	diags, err := validateVmDeep(body)
	if err != nil {
		t.Fatalf("validateVmDeep: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("expected no diagnostics, got %+v", diags.Items)
	}
}

// TestValidateVmDeep_EmptyStringSourceValue_Rejected proves the check catches an EMPTY-STRING
// authored value the same as an omitted key — "present but empty" is not concrete either.
func TestValidateVmDeep_EmptyStringSourceValue_Rejected(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"libvirt": map[string]any{
			"devices": map[string]any{
				"hostdevs": []map[string]any{
					{
						"type":   "pci",
						"source": map[string]any{"domain": "0x0000", "bus": "0x01", "slot": "", "function": "0x0"},
					},
				},
			},
		},
	})
	diags, err := validateVmDeep(body)
	if err != nil {
		t.Fatalf("validateVmDeep: %v", err)
	}
	if len(diags.Items) != 1 || diags.Items[0].Path != "libvirt.devices.hostdevs[0].source.slot" {
		t.Fatalf("expected exactly 1 diagnostic for the empty-string slot, got %+v", diags.Items)
	}
}

// TestValidateVmDeep_NonPCIHostdevIncomplete_NoDiagnostics proves the check is scoped to
// type:pci only — a usb hostdev with no source sub-fields at all produces zero diagnostics.
func TestValidateVmDeep_NonPCIHostdevIncomplete_NoDiagnostics(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"libvirt": map[string]any{
			"devices": map[string]any{
				"hostdevs": []map[string]any{
					{"type": "usb", "source": map[string]any{"vendor": "0x1234"}},
				},
			},
		},
	})
	diags, err := validateVmDeep(body)
	if err != nil {
		t.Fatalf("validateVmDeep: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("expected no diagnostics for a non-pci hostdev, got %+v", diags.Items)
	}
}

// TestValidateVmDeep_NoHostdevsAuthored_NoDiagnostics proves the empty-body / no-hostdevs fast
// path is a genuine no-op.
func TestValidateVmDeep_NoHostdevsAuthored_NoDiagnostics(t *testing.T) {
	for _, body := range [][]byte{nil, []byte(`{}`), mustMarshal(t, map[string]any{"source": map[string]any{"kind": "cloud_image"}})} {
		diags, err := validateVmDeep(body)
		if err != nil {
			t.Fatalf("validateVmDeep(%s): %v", body, err)
		}
		if diags.HasErrors() {
			t.Fatalf("validateVmDeep(%s): expected no diagnostics, got %+v", body, diags.Items)
		}
	}
}
