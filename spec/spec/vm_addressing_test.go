package spec

import "testing"

// TestSplitVmAddress is the table test for the "vm:"-prefixed CLI-addressing helper — the single
// source for detecting/stripping the prefix (relocated from sdk/vmshared/vm_deploy_addressing_test.go
// with the func itself, #55 vmshared Bucket B; vmshared keeps a re-export forwarder).
func TestSplitVmAddress(t *testing.T) {
	cases := []struct {
		name      string
		addr      string
		wantPlain string
		wantIsVm  bool
	}{
		{"vm:-prefixed top-level", "vm:myvm", "myvm", true},
		{"vm:-prefixed dotted", "vm:check-sidecar-pod.check-sidecar-pod-ephvm", "check-sidecar-pod.check-sidecar-pod-ephvm", true},
		{"unprefixed top-level", "myvm", "myvm", false},
		{"unprefixed dotted", "check-sidecar-pod.check-sidecar-pod-ephvm", "check-sidecar-pod.check-sidecar-pod-ephvm", false},
		{"bare \"vm:\" with nothing after it", "vm:", "", true},
		{"empty string", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plain, isVm := SplitVmAddress(tc.addr)
			if plain != tc.wantPlain || isVm != tc.wantIsVm {
				t.Errorf("SplitVmAddress(%q) = (%q, %v), want (%q, %v)", tc.addr, plain, isVm, tc.wantPlain, tc.wantIsVm)
			}
		})
	}
}

// TestVmNameFromDeployName covers the legacy "vm:<name>[/<instance>]" entity-extraction form.
func TestVmNameFromDeployName(t *testing.T) {
	cases := []struct {
		name    string
		deploy  string
		want    string
		wantErr bool
	}{
		{"bare vm-prefixed", "vm:arch", "arch", false},
		{"instance-suffixed", "vm:arch/work", "arch", false},
		{"missing vm-name portion", "vm:", "", true},
		{"no vm: prefix", "arch", "", true},
		{"empty string", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := VmNameFromDeployName(tc.deploy)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("VmNameFromDeployName(%q) = %q, nil; want an error", tc.deploy, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("VmNameFromDeployName(%q) unexpected error: %v", tc.deploy, err)
			}
			if got != tc.want {
				t.Errorf("VmNameFromDeployName(%q) = %q, want %q", tc.deploy, got, tc.want)
			}
		})
	}
}
