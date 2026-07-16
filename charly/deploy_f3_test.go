package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// TestPersistBedDeployOverrides_SkipsLocalBed pins the bed-infra fix for the
// cross-project host-overlay pollution. persistBedDeployOverrides exists ONLY to
// seed a POD bed's `charly config` step (port/volume/env overrides). A LOCAL bed
// never runs `charly config` (kind:local applies in place during `charly bundle
// add`), and its only persistable cross-ref is a `local:` template defined in the
// bed's OWN project — writing that into the GLOBAL per-host overlay makes the
// overlay un-loadable from every OTHER project (validateCheckBeds rejects the
// unresolvable template), which broke concurrent check-pod / check-k3s-vm runs that
// loaded the overlay while a check-local bed run had seeded its entry. Local deploys
// persist via the install ledger, not this bundle-map path, so the skip is lossless.
//
// The test proves the skip is exact: a local bed leaves the overlay untouched (still
// loadable, no entry), while a pod bed is still persisted.
func TestPersistBedDeployOverrides_SkipsLocalBed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte("version: "+LatestSchemaVersion().String()+"\n"), 0o600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// A LOCAL bed — persisting it would write an un-loadable `local:` cross-ref.
	disp := true
	persistBedDeployOverrides("check-local", BundleNode{
		Target:     "local",
		From:       "check-local-app",
		Disposable: &disp,
		Lifecycle:  "dev",
	})

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("overlay unloadable after local-bed persist (it should have been SKIPPED): %v", err)
	}
	if dc != nil {
		if _, ok := dc.Bundle["check-local"]; ok {
			t.Error("local bed was persisted to the global overlay — must be skipped (cross-project pollution)")
		}
	}

	// A POD bed is STILL persisted (the skip is not too broad). Non-disposable so it
	// is a plain deploy entry, not a check bed subject to validateCheckBeds.
	persistBedDeployOverrides("pod-deploy-x", BundleNode{
		Target: "pod",
		Image:  "pod-deploy-x",
	})
	dc2, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after pod-bed persist: %v", err)
	}
	if _, ok := dc2.Bundle["pod-deploy-x"]; !ok {
		t.Error("pod bed was NOT persisted — the local skip is too broad")
	}
}

// TestPersistBedDeployOverrides_RoundtripsArbiterFields pins the group-member
// resource-arbitration persistence fix. persistBedDeployOverrides must seed a
// member's arbiter role — the holder-side preemptible block and the claimant-side
// requires_exclusive / requires_shared token lists — into the per-host overlay, so
// the member's subsequent `charly start` reloads them and the arbiter actually
// fires (start.go → acquireResourceForClaimant; preempt.go's holder gather).
//
// Before the fix, saveDeployState dropped all three, so a reloaded member had
// RequiredExclusive()==[] / IsPreemptible()==false and the arbiter silently
// no-op'd — the empty-ledger RCA the C9 cutover surfaced (why check-preempt-arbiter-pod
// had to make the BED ROOT the claimant instead of a member). The check-preempt-live-pod
// group bed (a preemptible holder member actually stopped by a requires_exclusive
// claimant member) depends on this round-trip. This test FAILS without the fix.
func TestPersistBedDeployOverrides_RoundtripsArbiterFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte("version: "+LatestSchemaVersion().String()+"\n"), 0o600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// The two roles of the group live-preemption bed: a requires_exclusive CLAIMANT
	// member and a preemptible HOLDER member.
	persistBedDeployOverrides("preempt-taker", BundleNode{
		Target:            "pod",
		Image:             "check-pod",
		RequiresExclusive: []string{"test-lock"},
	})
	persistBedDeployOverrides("preempt-holder", BundleNode{
		Target:      "pod",
		Image:       "check-pod",
		Preemptible: &PreemptibleConfig{Holds: []string{"test-lock"}, Restore: "always"},
	})

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload per-host overlay: %v", err)
	}

	taker, ok := dc.Bundle["preempt-taker"]
	if !ok {
		t.Fatal("claimant member was not persisted")
	}
	if got := taker.RequiredExclusive(); len(got) != 1 || got[0] != "test-lock" {
		t.Errorf("claimant lost requires_exclusive on round-trip: got %v, want [test-lock] — the arbiter would no-op for this member", got)
	}

	holder, ok := dc.Bundle["preempt-holder"]
	if !ok {
		t.Fatal("holder member was not persisted")
	}
	if !holder.IsPreemptible() {
		t.Errorf("holder lost preemptible on round-trip: IsPreemptible()=false (holds=%v), want true — the arbiter would not gather this holder from the overlay", holder.PreemptionHolds())
	}
	if got := holder.PreemptionHolds(); len(got) != 1 || got[0] != "test-lock" {
		t.Errorf("holder lost preemptible.holds on round-trip: got %v, want [test-lock]", got)
	}
}
