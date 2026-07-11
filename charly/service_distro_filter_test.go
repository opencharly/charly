package main

import "testing"

// serviceEntryAppliesToDistro is the render-time filter that lets ONE candy
// carry per-distro-DIVERGENT service entries (the modular virtqemud.socket +
// virtnetworkd.socket on Fedora/Arch vs the monolithic libvirtd.socket on
// Debian/Ubuntu, whose libvirt is built without the split daemons) without a
// <name>-host sibling candy (CLAUDE.md R3). These tests pin its semantics and
// prove compileServiceSteps emits the right daemon set per (distro, init).

func TestServiceEntryAppliesToDistro(t *testing.T) {
	cases := []struct {
		name    string
		entry   []string // entry.Distro
		distros []string // target tag chain
		want    bool
	}{
		{"empty list applies everywhere (debian)", nil, []string{"debian:13", "debian"}, true},
		{"empty list applies everywhere (fedora)", nil, []string{"fedora:43", "fedora"}, true},
		{"bare name matches versioned tag (fedora)", []string{"fedora", "arch"}, []string{"fedora:43", "fedora"}, true},
		{"bare name matches bare tag (arch)", []string{"fedora", "arch"}, []string{"arch"}, true},
		{"fedora/arch list excludes debian", []string{"fedora", "arch"}, []string{"debian:13", "debian"}, false},
		{"debian/ubuntu list matches debian", []string{"debian", "ubuntu"}, []string{"debian:13", "debian"}, true},
		{"debian/ubuntu list matches ubuntu versioned", []string{"debian", "ubuntu"}, []string{"ubuntu:24.04", "ubuntu"}, true},
		{"debian/ubuntu list excludes fedora", []string{"debian", "ubuntu"}, []string{"fedora:43", "fedora"}, false},
		{"exact versioned tag match", []string{"debian:13"}, []string{"debian:13", "debian"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := &ServiceEntry{Name: "x", Distro: tc.entry}
			if got := serviceEntryAppliesToDistro(entry, tc.distros); got != tc.want {
				t.Fatalf("serviceEntryAppliesToDistro(%v, %v) = %v, want %v", tc.entry, tc.distros, got, tc.want)
			}
		})
	}
}

// virtualizationServiceEntries mirrors the real candy/virtualization service:
// list — the modular Fedora/Arch daemons + the monolithic Debian/Ubuntu one.
func virtualizationServiceEntries() []ServiceEntry {
	return []ServiceEntry{
		{Name: "virtqemud", UsePackaged: "virtqemud.socket", Distro: []string{"fedora", "arch"}, Enable: true, Scope: "system"},
		{Name: "virtqemud", Exec: "/usr/sbin/virtqemud --timeout 0", Distro: []string{"fedora", "arch"}, Enable: true, Scope: "system"},
		{Name: "virtnetworkd", UsePackaged: "virtnetworkd.socket", Distro: []string{"fedora", "arch"}, Enable: true, Scope: "system"},
		{Name: "virtnetworkd", Exec: "/usr/sbin/virtnetworkd --timeout 0", Distro: []string{"fedora", "arch"}, Enable: true, Scope: "system"},
		{Name: "libvirtd", UsePackaged: "libvirtd.socket", Distro: []string{"debian", "ubuntu"}, Enable: true, Scope: "system"},
		{Name: "libvirtd", Exec: "/usr/sbin/libvirtd --timeout 0", Distro: []string{"debian", "ubuntu"}, Enable: true, Scope: "system"},
	}
}

func packagedUnits(steps []InstallStep) []string {
	var out []string
	for _, s := range steps {
		if ps, ok := s.(*ServicePackagedStep); ok {
			out = append(out, ps.Unit)
		}
	}
	return out
}

func customServiceCount(steps []InstallStep) int {
	n := 0
	for _, s := range steps {
		if _, ok := s.(*ServiceCustomStep); ok {
			n++
		}
	}
	return n
}

func TestCompileServiceSteps_DistroDivergentDaemons(t *testing.T) {
	layer := &Candy{Name: "virtualization", service: virtualizationServiceEntries()}

	t.Run("debian systemd (vm) enables only libvirtd.socket", func(t *testing.T) {
		img := &ResolvedBox{Name: "debian-coder", Distro: []string{"debian:13", "debian"}}
		steps := compileServiceSteps(layer, img, HostContext{MachineVenue: true})
		units := packagedUnits(steps)
		if len(units) != 1 || units[0] != "libvirtd.socket" {
			t.Fatalf("debian systemd packaged units = %v, want [libvirtd.socket]", units)
		}
		// The libvirtd exec sibling is suppressed by the mixed-pair filter on
		// systemd; the fedora/arch exec entries are filtered out by distro.
		if n := customServiceCount(steps); n != 0 {
			t.Fatalf("debian systemd custom steps = %d, want 0", n)
		}
	})

	t.Run("fedora systemd (vm) enables the modular sockets", func(t *testing.T) {
		img := &ResolvedBox{Name: "fedora-coder", Distro: []string{"fedora:43", "fedora"}}
		steps := compileServiceSteps(layer, img, HostContext{MachineVenue: true})
		units := packagedUnits(steps)
		want := map[string]bool{"virtqemud.socket": true, "virtnetworkd.socket": true}
		if len(units) != 2 || !want[units[0]] || !want[units[1]] {
			t.Fatalf("fedora systemd packaged units = %v, want virtqemud.socket + virtnetworkd.socket", units)
		}
	})

	t.Run("debian supervisord (oci) runs only the libvirtd exec daemon", func(t *testing.T) {
		img := &ResolvedBox{Name: "debian-coder", Distro: []string{"debian:13", "debian"}}
		steps := compileServiceSteps(layer, img, HostContext{})
		if len(packagedUnits(steps)) != 0 {
			t.Fatalf("debian supervisord must emit no packaged units, got %v", packagedUnits(steps))
		}
		if n := customServiceCount(steps); n != 1 {
			t.Fatalf("debian supervisord custom steps = %d, want 1 (libvirtd)", n)
		}
	})

	t.Run("fedora supervisord (oci) runs the two modular exec daemons", func(t *testing.T) {
		img := &ResolvedBox{Name: "fedora-coder", Distro: []string{"fedora:43", "fedora"}}
		steps := compileServiceSteps(layer, img, HostContext{})
		if n := customServiceCount(steps); n != 2 {
			t.Fatalf("fedora supervisord custom steps = %d, want 2 (virtqemud + virtnetworkd)", n)
		}
	})

	// Regression guard for the R10 failure: a `target: vm` deploy compiles the
	// plan with the GUEST img (syntheticVmBox → img.Distro=[debian:13,debian])
	// but detectHostContext() defaults hostCtx.MachineVenue=true + the OPERATOR's
	// distro (e.g. "arch"). The service filter MUST scope to the guest (img),
	// NOT the operator host — else it keeps the modular [fedora,arch] virtqemud
	// entries and `systemctl enable virtqemud.socket` fails on the debian guest.
	t.Run("vm deploy: guest img wins over operator host distro", func(t *testing.T) {
		img := &ResolvedBox{Name: "vm-adhoc", Distro: []string{"debian:13", "debian"}}
		// The exact hostCtx a vm deploy carries: Target=host, Distro=<operator>.
		steps := compileServiceSteps(layer, img, HostContext{MachineVenue: true, Distro: "arch"})
		units := packagedUnits(steps)
		if len(units) != 1 || units[0] != "libvirtd.socket" {
			t.Fatalf("vm-on-arch-host packaged units = %v, want [libvirtd.socket] (guest wins, NOT operator arch)", units)
		}
	})
}

func TestServiceRenderDistros_ImgIsAuthoritative(t *testing.T) {
	cases := []struct {
		name    string
		img     []string
		hostCtx HostContext
		want    []string
	}{
		{
			// The R10-failing case: vm deploy carries hostCtx{host, arch} but the
			// guest img is debian. img MUST win.
			name:    "vm deploy guest debian beats operator arch",
			img:     []string{"debian:13", "debian"},
			hostCtx: HostContext{MachineVenue: true, Distro: "arch"},
			want:    []string{"debian:13", "debian"},
		},
		{
			// Host deploy: img (from syntheticHostBox) == the operator chain;
			// byte-identical to the old hostCtx.Distro path, plus the full chain
			// (so cachyos also matches `arch:` entries).
			name:    "host deploy uses the operator img chain",
			img:     []string{"cachyos", "arch"},
			hostCtx: HostContext{MachineVenue: true, Distro: "cachyos"},
			want:    []string{"cachyos", "arch"},
		},
		{
			// Degenerate: no img distro → fall back to hostCtx.
			name:    "empty img falls back to hostCtx",
			img:     nil,
			hostCtx: HostContext{MachineVenue: true, Distro: "arch"},
			want:    []string{"arch"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serviceRenderDistros(&ResolvedBox{Distro: tc.img}, tc.hostCtx)
			if len(got) != len(tc.want) {
				t.Fatalf("serviceRenderDistros = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("serviceRenderDistros = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestPrimaryDistroTag_ImgIsAuthoritative(t *testing.T) {
	// vm deploy: guest img debian must win over operator arch hostCtx.
	if got := primaryDistroTag(&ResolvedBox{Distro: []string{"debian:13", "debian"}}, HostContext{MachineVenue: true, Distro: "arch"}); got != "debian:13" {
		t.Fatalf("vm-deploy primaryDistroTag = %q, want debian:13 (guest, not operator arch)", got)
	}
	// host deploy: img chain == operator; byte-identical result.
	if got := primaryDistroTag(&ResolvedBox{Distro: []string{"arch"}}, HostContext{MachineVenue: true, Distro: "arch"}); got != "arch" {
		t.Fatalf("host primaryDistroTag = %q, want arch", got)
	}
	// degenerate fallback.
	if got := primaryDistroTag(&ResolvedBox{}, HostContext{MachineVenue: true, Distro: "arch"}); got != "arch" {
		t.Fatalf("empty-img primaryDistroTag = %q, want arch (fallback)", got)
	}
}
