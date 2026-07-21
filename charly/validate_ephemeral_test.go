package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestValidateEphemeralOnNode_PodK8sRejected is the check-coverage gate for the load-time guard
// added in FINAL/K5 unit 6a: the ephemeral register/teardown mechanism is wired for the vm
// substrate ONLY (registerEphemeralIfMarked's sole caller is vm_lifecycle_preresolve.go) — pod
// and k8s Add/Del never reach it, so `ephemeral: true` on either was previously accepted at
// load time and then silently INERT at runtime. This test would FAIL without the fix (the prior
// behavior let pod/k8s ephemeral through with no error).
func TestValidateEphemeralOnNode_PodK8sRejected(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"pod substrate", "pod"},
		{"container substrate", "container"},
		{"k8s substrate", "k8s"},
		{"kubernetes substrate", "kubernetes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &spec.BundleNode{Target: tc.target, Ephemeral: &spec.EphemeralLifetime{}}
			errs := &ValidationError{}
			ValidateEphemeralOnNode("test-deploy", node, errs)
			if !errs.HasErrors() {
				t.Fatalf("target=%q with ephemeral: true: want a validation error, got none", tc.target)
			}
			if !strings.Contains(errs.Error(), "not yet supported") {
				t.Errorf("target=%q error = %q, want it to mention 'not yet supported'", tc.target, errs.Error())
			}
		})
	}
}

// TestValidateEphemeralOnNode_VmAccepted proves the guard does NOT reject the one substrate
// that actually wires the ephemeral lifecycle today.
func TestValidateEphemeralOnNode_VmAccepted(t *testing.T) {
	node := &spec.BundleNode{Target: "vm", Ephemeral: &spec.EphemeralLifetime{}}
	errs := &ValidationError{}
	ValidateEphemeralOnNode("test-vm-deploy", node, errs)
	if errs.HasErrors() {
		t.Errorf("target=vm with ephemeral: true: want no error, got %q", errs.Error())
	}
}
