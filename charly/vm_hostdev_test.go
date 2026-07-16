package main

import (
	"github.com/opencharly/sdk/spec"
	"testing"
)

// TestVmHostdevCount pins the nil-safety contract of the VM_HOSTDEV_COUNT
// intent source: a spec with no libvirt block, no devices block, or an empty
// hostdevs list all read as 0 ("no GPU configured for this VM" → legit N/A),
// and a declared hostdevs list reports its length (the GPU check check then
// HARD-FAILS if the guest can't see the device).
func TestVmHostdevCount(t *testing.T) {
	cases := []struct {
		name string
		spec *VmSpec
		want int
	}{
		{"nil spec", nil, 0},
		{"nil libvirt", &VmSpec{}, 0},
		{"nil devices", &VmSpec{Libvirt: &spec.LibvirtDomain{}}, 0},
		{"zero hostdevs", &VmSpec{Libvirt: &spec.LibvirtDomain{Devices: &LibvirtDevices{}}}, 0},
		{"two hostdevs", &VmSpec{Libvirt: &spec.LibvirtDomain{Devices: &LibvirtDevices{
			Hostdevs: []LibvirtHostdev{{}, {}},
		}}}, 2},
	}
	for _, tc := range cases {
		if got := vmHostdevCount(tc.spec); got != tc.want {
			t.Errorf("%s: vmHostdevCount = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestVmHostdevCountIsRuntimeOnly guards the validation contract: VM_HOSTDEV_COUNT
// resolves only against a live VM deployment, so a scope:"build" check must be
// barred from referencing it (validate_check.go enforces this via IsRuntimeOnlyVar).
func TestVmHostdevCountIsRuntimeOnly(t *testing.T) {
	if !IsRuntimeOnlyVar("VM_HOSTDEV_COUNT") {
		t.Error("VM_HOSTDEV_COUNT must be runtime-only so build-scope checks cannot reference it")
	}
}
