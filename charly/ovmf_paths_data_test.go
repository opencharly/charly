package main

import (
	"os"
	"testing"

	"github.com/opencharly/sdk/vmshared"
)

// TestOvmfPathsFromEmbedded is the drift-guard: ovmfCandidatesForDistro returns
// byte-identical ordered candidates from the embedded charly.yml ovmf_paths directive
// (Phase 4: data moved out of Go) — alias resolution (centos→fedora), secure selection,
// and the unknown-distro union all preserved.
func TestOvmfPathsFromEmbedded(t *testing.T) {
	eq := func(t *testing.T, got []vmshared.OvmfPaths, want ...[2]string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %d candidates, want %d: %+v", len(got), len(want), got)
		}
		for i, w := range want {
			if got[i].CodePath != w[0] || got[i].VarsTemplate != w[1] {
				t.Fatalf("candidate[%d] = {%q,%q}, want {%q,%q}", i, got[i].CodePath, got[i].VarsTemplate, w[0], w[1])
			}
		}
	}

	eq(t, vmshared.OvmfCandidatesForDistro("fedora", true),
		[2]string{"/usr/share/OVMF/OVMF_CODE.secboot.fd", "/usr/share/OVMF/OVMF_VARS.secboot.fd"},
		[2]string{"/usr/share/edk2/ovmf/OVMF_CODE.secboot.fd", "/usr/share/edk2/ovmf/OVMF_VARS.secboot.fd"})

	eq(t, vmshared.OvmfCandidatesForDistro("arch", false),
		[2]string{"/usr/share/edk2/x64/OVMF_CODE.4m.fd", "/usr/share/edk2/x64/OVMF_VARS.4m.fd"},
		[2]string{"/usr/share/edk2-ovmf/x64/OVMF_CODE.fd", "/usr/share/edk2-ovmf/x64/OVMF_VARS.fd"})

	eq(t, vmshared.OvmfCandidatesForDistro("debian", true),
		[2]string{"/usr/share/OVMF/OVMF_CODE_4M.ms.fd", "/usr/share/OVMF/OVMF_VARS_4M.ms.fd"},
		[2]string{"/usr/share/OVMF/OVMF_CODE.secboot.fd", "/usr/share/OVMF/OVMF_VARS.secboot.fd"})

	if got := vmshared.OvmfCandidatesForDistro("centos", false); len(got) != 2 || got[0].CodePath != "/usr/share/OVMF/OVMF_CODE.fd" {
		t.Fatalf("centos→fedora alias broken: %+v", got)
	}
	if got := vmshared.OvmfCandidatesForDistro("gentoo", false); len(got) != 6 {
		t.Fatalf("unknown-distro union = %d candidates, want 6: %+v", len(got), got)
	}
}

// TestResolveOvmfPaths_HostFirmwareResolves is the RUNTIME R10: it runs the exact resolution
// path `charly vm create` uses — ResolveOvmfPaths(hostDistro, secure) → ovmfCandidatesForDistro
// (now data-driven) → os.Stat each candidate — against the REAL host firmware, and asserts it
// returns an existing OVMF_CODE/OVMF_VARS pair. This proves the data-driven paths resolve to
// real files on this host (where the UEFI VM beds boot). Skips only if the host has no
// edk2-ovmf installed (then no UEFI VM could boot here anyway).
func TestResolveOvmfPaths_HostFirmwareResolves(t *testing.T) {
	hd, err := DetectHostDistro()
	if err != nil {
		t.Skipf("cannot detect host distro: %v", err)
	}
	paths, err := vmshared.ResolveOvmfPaths(hd.ID, false)
	if err != nil {
		t.Skipf("host %q has no OVMF firmware (edk2-ovmf) installed: %v", hd.ID, err)
	}
	if _, err := os.Stat(paths.CodePath); err != nil {
		t.Fatalf("data-driven resolved OVMF_CODE %q does not exist: %v", paths.CodePath, err)
	}
	if _, err := os.Stat(paths.VarsTemplate); err != nil {
		t.Fatalf("data-driven resolved OVMF_VARS %q does not exist: %v", paths.VarsTemplate, err)
	}
	t.Logf("host %q OVMF resolved from embedded data: code=%s vars=%s", hd.ID, paths.CodePath, paths.VarsTemplate)
}
