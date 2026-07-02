package main

import (
	"testing"
)

// A richly-populated vm entity exercising every subtree applyCueDefaults must
// preserve losslessly: source, network, ssh, cpu, libvirt devices (hostdev +
// filesystem), and cloud_init. firmware is left unset so the schema default
// must materialize.
const fidelityVmYAML = `
disk_size: 20G
ram: 4G
cpu: 4
machine: q35
backend: libvirt
source:
  kind: cloud_image
  url: https://example.com/arch.qcow2
  base_user: arch
network:
  mode: bridge
  bridge: br0
  model: virtio-net-pci
  port_forwards:
  - "2222:22"
ssh:
  port: 2244
  key_source: generate
cloud_init:
  hostname: testvm
  users:
  - name: arch
    sudo: true
    groups: [wheel]
libvirt:
  cpu:
    mode: custom
    model: host
    features:
    - name: vmx
      policy: require
  devices:
    hostdevs:
    - type: pci
      managed: "yes"
      source:
        domain: "0x0000"
        bus: "0x01"
        slot: "0x00"
        function: "0x0"
    filesystems:
    - driver: virtiofs
      accessmode: passthrough
      source: /home/arch/work
      target: workspace
`

// defaultedFidelitySpec unmarshals the fixture (firmware unset) and runs
// applyCueDefaults, returning the result for the per-subtree fidelity checks.
func defaultedFidelitySpec(t *testing.T) *VmSpec {
	t.Helper()
	var spec VmSpec
	if err := decodeViaCUEForTest(t, fidelityVmYAML, &spec); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if spec.Firmware != "" {
		t.Fatalf("fixture should leave firmware unset, got %q", spec.Firmware)
	}
	if err := applyCueDefaults("vm", &spec); err != nil {
		t.Fatalf("applyCueDefaults: %v", err)
	}
	return &spec
}

func TestApplyCueDefaults_FillsFirmware(t *testing.T) {
	if got := defaultedFidelitySpec(t).Firmware; got != "bios" {
		t.Errorf("firmware: want bios (schema default), got %q", got)
	}
}

func TestApplyCueDefaults_PreservesScalars(t *testing.T) {
	spec := defaultedFidelitySpec(t)
	cases := map[string]struct{ got, want any }{
		"disk_size":        {spec.DiskSize, "20G"},
		"ram":              {spec.Ram, "4G"},
		"cpu":              {spec.Cpus, 4},
		"machine":          {spec.Machine, "q35"},
		"backend":          {spec.Backend, "libvirt"},
		"source.kind":      {spec.Source.Kind, "cloud_image"},
		"source.url":       {spec.Source.URL, "https://example.com/arch.qcow2"},
		"source.base_user": {spec.Source.BaseUser, "arch"},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: want %v, got %v", name, c.want, c.got)
		}
	}
}

func TestApplyCueDefaults_PreservesNetwork(t *testing.T) {
	n := defaultedFidelitySpec(t).Network
	if n == nil {
		t.Fatal("network dropped")
	}
	if n.Mode != "bridge" || n.Bridge != "br0" || n.Model != "virtio-net-pci" {
		t.Errorf("network scalars not preserved: %+v", n)
	}
	if len(n.PortForwards) != 1 || n.PortForwards[0] != "2222:22" {
		t.Errorf("network port_forwards not preserved: %+v", n.PortForwards)
	}
}

func TestApplyCueDefaults_PreservesSSHAndCloudInit(t *testing.T) {
	spec := defaultedFidelitySpec(t)
	if s := spec.SSH; s == nil || s.Port != 2244 || s.KeySource != "generate" {
		t.Errorf("ssh subtree not preserved: %+v", spec.SSH)
	}
	ci := spec.CloudInit
	if ci == nil || ci.Hostname != "testvm" || len(ci.Users) != 1 {
		t.Fatalf("cloud_init subtree not preserved: %+v", ci)
	}
	if u := ci.Users[0]; u.Name != "arch" || !u.Sudo || len(u.Groups) != 1 {
		t.Errorf("cloud_init user not preserved: %+v", u)
	}
}

func TestApplyCueDefaults_PreservesLibvirtCPU(t *testing.T) {
	lv := defaultedFidelitySpec(t).Libvirt
	if lv == nil || lv.CPU == nil {
		t.Fatal("libvirt.cpu dropped")
	}
	if lv.CPU.Mode != "custom" || lv.CPU.Model != "host" || len(lv.CPU.Features) != 1 {
		t.Fatalf("libvirt.cpu not preserved: %+v", lv.CPU)
	}
	if f := lv.CPU.Features[0]; f.Name != "vmx" || f.Policy != "require" {
		t.Errorf("libvirt.cpu feature not preserved: %+v", f)
	}
}

func TestApplyCueDefaults_PreservesLibvirtDevices(t *testing.T) {
	lv := defaultedFidelitySpec(t).Libvirt
	if lv == nil || lv.Devices == nil {
		t.Fatal("libvirt.devices dropped")
	}
	if len(lv.Devices.Hostdevs) != 1 {
		t.Fatalf("hostdevs not preserved: %+v", lv.Devices)
	}
	if hd := lv.Devices.Hostdevs[0]; hd.Type != "pci" || hd.Managed != "yes" ||
		hd.Source["bus"] != "0x01" || hd.Source["function"] != "0x0" {
		t.Errorf("hostdev not preserved: %+v", lv.Devices.Hostdevs[0])
	}
	if len(lv.Devices.Filesystems) != 1 {
		t.Fatalf("filesystems not preserved: %+v", lv.Devices)
	}
	if fs := lv.Devices.Filesystems[0]; fs.Driver != "virtiofs" || fs.AccessMode != "passthrough" ||
		fs.Source != "/home/arch/work" || fs.Target != "workspace" {
		t.Errorf("filesystem not preserved: %+v", lv.Devices.Filesystems[0])
	}
}

// A vm that explicitly sets firmware must keep it (default never clobbers).
func TestApplyCueDefaults_VmPreservesSetFirmware(t *testing.T) {
	var spec VmSpec
	if err := decodeViaCUEForTest(t, "firmware: uefi-insecure\nsource:\n  kind: cloud_image\n  url: https://x/i.qcow2\n", &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := applyCueDefaults("vm", &spec); err != nil {
		t.Fatalf("applyCueDefaults: %v", err)
	}
	if spec.Firmware != "uefi-insecure" {
		t.Errorf("set firmware must be preserved, got %q", spec.Firmware)
	}
}
