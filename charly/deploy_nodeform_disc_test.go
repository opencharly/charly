package main

import "testing"

// A deploy with an EMPTY target but a POD-WORKLOAD indicator (an image, a resolved pod-port
// map, or an authored port) is a POD — the default substrate — NOT a targetless group.
// Misclassifying it as group writes the pod-only resolved_port under group:, which #GroupInput
// rejects at the next load (the 2026-07 config corruption: `kind:group: #GroupInput.resolved_port
// field not allowed`). Only a truly members-only deploy (no own workload) stays a group.
func TestBundleDiscForEntity_PodWorkloadNotGroup(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"resolved_port -> pod", "resolved_port:\n  - 36531:2222\n  - 39391:3000\n", "pod"},
		{"image -> pod", "image: ghcr.io/opencharly/x:latest\n", "pod"},
		{"authored port -> pod", "port:\n  - 8080:80\n", "pod"},
		{"no workload -> group", "disposable: true\n", "group"},
		{"explicit host target -> local", "target: host\n", "local"},
		{"explicit pod target -> pod", "target: pod\n", "pod"},
		// FINAL/K5 unit 6a: the SAME bug class as the resolved_port case above, live-bed-caught
		// this time via candy/plugin-bundle's persistEphemeralRuntime writing an ephemeral
		// vm's per-host overlay entry with vm_state set but Target left EMPTY (a bare
		// spec.BundleNode{} fallback on first registration) — #GroupInput rejected the
		// leftover vm_state field on the next reload. This case documents the FAILURE MODE
		// the fix (ephemeralFallbackNode, candy/plugin-bundle/ephemeral.go) avoids: an
		// empty-target node carrying ONLY vm_state (no pod-workload indicator) still
		// discriminates as "group", exactly like the empty case above — proving Target must
		// be seeded explicitly, a workload-indicator heuristic cannot rescue this shape.
		{"vm_state without target -> group (the pre-fix failure mode)", "vm_state:\n  ephemeral:\n    id: abc123\n    status: active\n", "group"},
		// The fixed shape: Target explicitly seeded (ephemeralFallbackNode's job) resolves
		// correctly regardless of vm_state's presence.
		{"target: vm with vm_state -> vm (the fixed shape)", "target: vm\nvm_state:\n  ephemeral:\n    id: abc123\n    status: active\n", "vm"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bundleDiscForEntity(mustYAMLNode(t, c.yaml)); got != c.want {
				t.Errorf("bundleDiscForEntity(%q) = %q, want %q", c.yaml, got, c.want)
			}
		})
	}
}
