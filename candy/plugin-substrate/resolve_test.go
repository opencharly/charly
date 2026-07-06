package substratekind

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestResolveVm_FieldCopy covers the vm substrate-value de-type (Cutover L): OpResolve
// projects spec.Vm → ResolvedVm (field-copy + Raw). Without the resolve leg the kernel's
// vm build/deploy consumers cannot read the de-typed, opaque vm template.
func TestResolveVm_FieldCopy(t *testing.T) {
	body, err := json.Marshal(spec.Vm{Backend: "libvirt", Cpus: 4, Ram: "8G", Firmware: "uefi"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := resolveSubstrateTemplate(spec.SubstrateTemplateResolveRequest{
		Vm: &spec.VmResolveInput{Vm: body},
	})
	if err != nil {
		t.Fatalf("resolveSubstrateTemplate(vm): %v", err)
	}
	var reply spec.VmResolveReply
	if err := json.Unmarshal(out, &reply); err != nil {
		t.Fatal(err)
	}
	r := reply.Resolved
	if r == nil || r.Backend != "libvirt" || r.Cpus != 4 || r.Ram != "8G" || r.Firmware != "uefi" {
		t.Fatalf("vm field copy failed: %+v", r)
	}
	if string(r.Raw) != string(body) {
		t.Errorf("Raw not preserved through resolve")
	}
}
