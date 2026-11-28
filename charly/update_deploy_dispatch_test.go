package main

import "testing"

// TestUpdateDeployName pins opencharly/opencharly#1316's update leg: the rebuild re-deploys the
// deploy's IDENTITY (box + instance), not the box alone. Measured failure before the fix: a
// namespace-imported bed's update ran `charly deploy add kde-vnc-cdp` and died on
// `no <ref> and charly.yml has no entry`.
func TestUpdateDeployName(t *testing.T) {
	tests := []struct {
		name     string
		box      string
		instance string
		want     string
	}{
		{"a namespaced address carries both halves", "kde-vnc-cdp", "check-kde-vnc-cdp-pod", "kde-vnc-cdp/check-kde-vnc-cdp-pod"},
		{"an instance deploy carries its instance", "web", "prod", "web/prod"},
		{"a plain deploy stays its own key", "check-pod", "", "check-pod"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := updateDeployName(tc.box, tc.instance); got != tc.want {
				t.Fatalf("updateDeployName(%q, %q) = %q, want %q", tc.box, tc.instance, got, tc.want)
			}
		})
	}
}
