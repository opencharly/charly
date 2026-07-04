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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bundleDiscForEntity(mustYAMLNode(t, c.yaml)); got != c.want {
				t.Errorf("bundleDiscForEntity(%q) = %q, want %q", c.yaml, got, c.want)
			}
		})
	}
}
