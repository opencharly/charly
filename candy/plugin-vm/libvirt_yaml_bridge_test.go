package main

// Bridge tests — verify that LibvirtDomain (vms.yml shape) converts
// to libvirtxml.Domain and marshals to XML with the expected
// fragments. Covers:
//   - Top-level domain skeleton (name, memory, vcpu, on_*)
//   - arch's actual libvirt stanza (spice graphics,
//     spicevmc channel, virtio video+rng+memballoon)
//   - Each Rule 5 divergence (video.accel3d scalar, graphics.listen
//     scalar, memballoon.model scalar, rng.backend scalar).
//   - xml_passthrough fragment merge.

import (
	"strings"
	"testing"
)

func TestRenderDomainXML_Skeleton(t *testing.T) {
	spec := &VmSpec{
		Firmware: "bios",
		Machine:  "q35",
	}
	rt := VmRuntimeParams{
		Name:     "charly-smoke",
		RamMB:    2048,
		Cpus:     2,
		HostArch: "x86_64",
	}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := []string{
		`<domain type="kvm">`,
		`<name>charly-smoke</name>`,
		`<memory unit="MiB">2048</memory>`,
		`<currentMemory unit="MiB">2048</currentMemory>`,
		`<vcpu placement="static">2</vcpu>`,
		`<os>`,
		`<type arch="x86_64" machine="q35">hvm</type>`,
		`<boot dev="hd">`,
		`<features>`,
		`<acpi></acpi>`, // default-on
		`<apic></apic>`, // default-on
		`<clock offset="utc">`,
		`<on_poweroff>destroy</on_poweroff>`,
		`<on_reboot>restart</on_reboot>`,
		`<on_crash>destroy</on_crash>`,
	}
	for _, frag := range want {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
}

func TestRenderDomainXML_ArchCloudBase(t *testing.T) {
	// Mirror the vms.yml arch `libvirt:` stanza (post hard
	// cutover to socket-only SPICE — see libvirt_yaml_listen.go).
	yamlStr := `
devices:
  channels:
    - type: spicevmc
      name: com.redhat.spice.0
  graphics:
    - type: spice
      listen:
        - type: socket
  video:
    - model: virtio
      vram: 65536
      heads: 1
      accel3d: false
  rng:
    - model: virtio
      backend: /dev/urandom
  memballoon:
    model: virtio
`
	var lv LibvirtDomain
	if err := decodeViaCUEForTest(t, yamlStr, &lv); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	spec := &VmSpec{
		Firmware: "bios",
		Libvirt:  &lv,
	}
	rt := VmRuntimeParams{
		Name:     "charly-arch",
		RamMB:    8192,
		Cpus:     4,
		HostArch: "x86_64",
	}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := []string{
		// Graphics: socket-only listener. virt-manager and
		// `remote-viewer --connect qemu+ssh://…` auto-forward UNIX
		// sockets; no TCP exposure.
		`<graphics type="spice">`,
		`<listen type="socket">`,
		// Channel with spicevmc source + target virtio named.
		`<channel type="spicevmc">`,
		`<target type="virtio" name="com.redhat.spice.0">`,
		// Video: divergence #1 (accel3d scalar → nested <acceleration accel3d=...>)
		`<video>`,
		`<model type="virtio"`,
		`vram="65536"`,
		`heads="1"`,
		`<acceleration accel3d="no">`,
		// RNG: divergence #7 (backend scalar → nested <backend model="random">)
		`<rng model="virtio">`,
		`<backend model="random">/dev/urandom</backend>`,
		// MemBalloon: divergence #6 (model scalar → <memballoon model="...">)
		`<memballoon model="virtio">`,
	}
	for _, frag := range want {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
	// Explicit negative: no TCP listener for SPICE.
	if strings.Contains(out, `<listen type="address"`) {
		t.Errorf("expected no TCP <listen> for SPICE, got one in:\n%s", out)
	}
}

// TestRenderDomainXML_GraphicsListen_AllForms covers the three
// accepted YAML shapes for the `listen:` field on a <graphics>
// element: scalar, single mapping, and sequence.
func TestRenderDomainXML_GraphicsListen_AllForms(t *testing.T) {
	// charly's CUE loader coerces the polymorphic `listen:` YAML (scalar / map / list) into the
	// LibvirtGraphicsListeners slice (that coercion is covered by charly's loader tests); here we
	// pin that the RENDERER (RenderDomainXML) emits the right <listen> XML for each coerced shape.
	cases := []struct {
		name     string
		graphics LibvirtGraphics
		want     []string
		notWant  []string
	}{
		{
			name:     "scalar address → address listener",
			graphics: LibvirtGraphics{Type: "vnc", Listen: LibvirtGraphicsListeners{{Type: "address", Address: "127.0.0.1"}}},
			want: []string{
				`<graphics type="vnc"`,
				`<listen type="address" address="127.0.0.1">`,
			},
		},
		{
			name:     "socket-only mapping",
			graphics: LibvirtGraphics{Type: "spice", Listen: LibvirtGraphicsListeners{{Type: "socket"}}},
			want: []string{
				`<graphics type="spice">`,
				`<listen type="socket">`,
			},
			notWant: []string{
				`<listen type="address"`,
			},
		},
		{
			name:     "list of socket + address",
			graphics: LibvirtGraphics{Type: "spice", Listen: LibvirtGraphicsListeners{{Type: "socket"}, {Type: "address", Address: "127.0.0.1"}}},
			want: []string{
				`<graphics type="spice">`,
				`<listen type="socket">`,
				`<listen type="address" address="127.0.0.1">`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lv := LibvirtDomain{Devices: &LibvirtDevices{Graphics: []LibvirtGraphics{tc.graphics}}}
			out, err := RenderDomainXML(&VmSpec{Libvirt: &lv},
				VmRuntimeParams{Name: "charly-listen-test", RamMB: 512, Cpus: 1, HostArch: "x86_64"})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, frag := range tc.want {
				if !strings.Contains(out, frag) {
					t.Errorf("missing fragment %q in:\n%s", frag, out)
				}
			}
			for _, frag := range tc.notWant {
				if strings.Contains(out, frag) {
					t.Errorf("unexpected fragment %q present in:\n%s", frag, out)
				}
			}
		})
	}
}

func TestRenderDomainXML_XMLPassthrough(t *testing.T) {
	// Fragment with both a singleton (launchSecurity) and a device
	// (vsock).
	lv := &LibvirtDomain{
		XMLPassthrough: `
<launchSecurity type="sev-snp">
  <policy>0x30000</policy>
</launchSecurity>
<devices>
  <vsock model="virtio">
    <cid auto="yes"></cid>
  </vsock>
</devices>
`,
	}
	spec := &VmSpec{Libvirt: lv}
	rt := VmRuntimeParams{Name: "charly-passthrough", RamMB: 512, Cpus: 1, HostArch: "x86_64"}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := []string{
		`<launchSecurity type="sev-snp">`,
		`<vsock model="virtio">`,
	}
	for _, frag := range want {
		if !strings.Contains(out, frag) {
			t.Errorf("missing passthrough fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
}

func TestRenderDomainXML_AutoSynthesizedDisk(t *testing.T) {
	spec := &VmSpec{}
	rt := VmRuntimeParams{
		Name:        "charly-disk",
		RamMB:       1024,
		Cpus:        1,
		HostArch:    "x86_64",
		QCOW2Path:   "/tmp/charly-disk.qcow2",
		SeedISOPath: "/tmp/charly-disk.iso",
		SshPort:     2224,
	}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Root disk: vda, virtio, file=/tmp/charly-disk.qcow2.
	for _, frag := range []string{
		`<disk type="file" device="disk">`,
		`<driver name="qemu" type="qcow2">`,
		`<source file="/tmp/charly-disk.qcow2">`,
		`<target dev="vda" bus="virtio">`,
		// Seed ISO.
		`<disk type="file" device="cdrom">`,
		`<source file="/tmp/charly-disk.iso">`,
		`<target dev="sda" bus="sata">`,
		// Default user-mode interface + SSH forward via passt.
		`<interface type="user">`,
		`<backend type="passt">`,
		// host-only bind (security): VM forwards must never expose on 0.0.0.0/LAN.
		`<portForward proto="tcp" address="127.0.0.1">`,
		`<range start="2224" to="22">`,
		// Auto-synthesized serial + console (emitted with type="pty").
		`<serial type="pty">`,
		`<target port="0">`,
		`<console type="pty">`,
		`<target type="serial" port="0">`,
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
}

// TestRenderDomainXML_SmbiosSysinfoMode asserts that whenever an SMBIOS OEM
// credential is present, the domain ALSO carries <os><smbios mode="sysinfo"/>.
// Without that directive QEMU defines the OEM strings but never presents them
// to the guest's DMI, so systemd-creds/systemd-tmpfiles never see the
// `tmpfiles.extra` SSH-key credential and the SMBIOS injection channel is dead.
func TestRenderDomainXML_SmbiosSysinfoMode(t *testing.T) {
	spec := &VmSpec{}
	rt := VmRuntimeParams{
		Name:     "charly-smbios",
		RamMB:    512,
		Cpus:     1,
		HostArch: "x86_64",
		SMBIOSCredentials: []string{
			SmbiosCredForSSH("cachy", "/home/cachy", "ssh-ed25519 AAAATESTKEY user@host"),
		},
	}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "<oemStrings>") {
		t.Errorf("missing <oemStrings> SMBIOS credential\n--- output ---\n%s", out)
	}
	if !strings.Contains(out, `<smbios mode="sysinfo">`) {
		t.Errorf("missing <os><smbios mode=\"sysinfo\"/> — OEM credential never reaches the guest\n--- output ---\n%s", out)
	}
}

// TestRenderDomainXML_VirtiofsAutoSharedMemory verifies that declaring a
// virtiofs filesystem auto-pairs the shared-memory backing the device
// requires (memfd + access=shared) even when the entity declares none — so
// authors can't ship a virtiofs VM that silently fails to start.
func TestRenderDomainXML_VirtiofsAutoSharedMemory(t *testing.T) {
	yamlStr := `
devices:
  filesystems:
    - driver: virtiofs
      accessmode: passthrough
      source: /home/user
      target: workspace
`
	var lv LibvirtDomain
	if err := decodeViaCUEForTest(t, yamlStr, &lv); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	spec := &VmSpec{Firmware: "bios", Libvirt: &lv}
	rt := VmRuntimeParams{Name: "charly-ws", RamMB: 4096, Cpus: 2, HostArch: "x86_64"}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, frag := range []string{
		`<filesystem type="mount" accessmode="passthrough">`,
		`<driver type="virtiofs">`,
		`<source dir="/home/user">`,
		`<target dir="workspace">`,
		// Auto-injected shared memory backing (required by virtiofs).
		`<memoryBacking>`,
		`<source type="memfd">`,
		`<access mode="shared">`,
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
}

// TestRenderDomainXML_VirtiofsHonorsExplicitBacking verifies the auto-pairing
// does NOT duplicate or clobber an explicitly-declared memory backing — it
// only fills the missing source/access bits.
func TestRenderDomainXML_VirtiofsHonorsExplicitBacking(t *testing.T) {
	yamlStr := `
memory_backing:
  access: shared
  source: memfd
devices:
  filesystems:
    - driver: virtiofs
      source: /srv/data
      target: data
`
	var lv LibvirtDomain
	if err := decodeViaCUEForTest(t, yamlStr, &lv); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	spec := &VmSpec{Firmware: "bios", Libvirt: &lv}
	rt := VmRuntimeParams{Name: "charly-data", RamMB: 2048, Cpus: 1, HostArch: "x86_64"}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if n := strings.Count(out, "<memoryBacking>"); n != 1 {
		t.Errorf("expected exactly one <memoryBacking>, got %d\n%s", n, out)
	}
	for _, frag := range []string{`<source type="memfd">`, `<access mode="shared">`} {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
}

// TestRenderDomainXML_GuestAgentChannel verifies the structured
// channels: [{type: unix, name: org.qemu.guest_agent.0}] idiom renders a
// libvirt-managed unix channel (type="unix" + virtio target) — the path the
// kind:vm entity uses to wire the guest agent.
func TestRenderDomainXML_GuestAgentChannel(t *testing.T) {
	yamlStr := `
devices:
  channels:
    - type: unix
      name: org.qemu.guest_agent.0
`
	var lv LibvirtDomain
	if err := decodeViaCUEForTest(t, yamlStr, &lv); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	spec := &VmSpec{Firmware: "bios", Libvirt: &lv}
	rt := VmRuntimeParams{Name: "charly-aga", RamMB: 2048, Cpus: 1, HostArch: "x86_64"}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, frag := range []string{
		`<channel type="unix">`,
		`<target type="virtio" name="org.qemu.guest_agent.0">`,
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
}

// TestVmSpecAutostartYAMLRoundTrip verifies the `autostart:` key parses into
// VmSpec.Autostart (the field VmCreateCmd reads to set the libvirt flag).
func TestVmSpecAutostartYAMLRoundTrip(t *testing.T) {
	var on VmSpec
	if err := decodeViaCUEForTest(t, "autostart: true\nbackend: libvirt\n", &on); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !on.Autostart {
		t.Error("autostart: true did not parse into VmSpec.Autostart")
	}
	var off VmSpec
	if err := decodeViaCUEForTest(t, "backend: libvirt\n", &off); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if off.Autostart {
		t.Error("absent autostart should default to false")
	}
}
