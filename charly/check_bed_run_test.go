package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

// TestCheckBeds_DerivesFromDisposableBundles asserts the R10 bed set is derived
// from the `disposable: true` bundles in the Deploy map (the separate kind:check
// block was removed — a bed IS a disposable bundle); a non-disposable deploy is
// NOT a bed.
func TestCheckBeds_DerivesFromDisposableBundles(t *testing.T) {
	uf := &UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"sample-pod-bed":   {Target: "pod", Image: "sample-image", Disposable: new(true)},
			"sample-vm-bed":    {Target: "vm", From: "sample-vm", Disposable: new(true)},
			"sample-local-bed": {Target: "local", From: "sample-local", Disposable: new(true)},
			"plain-deploy":     {Target: "pod", Image: "prod"}, // not disposable → not a bed
		},
	}
	beds := uf.CheckBeds()
	if got := len(beds); got != 3 {
		t.Errorf("CheckBeds() = %d entries, want 3 (only disposable bundles)", got)
	}
	if _, ok := beds["plain-deploy"]; ok {
		t.Error("a non-disposable deploy must NOT be enumerated as a bed")
	}
}

// TestValidateCheckBeds_TargetEnum asserts an unsupported target is rejected.
func TestValidateCheckBeds_TargetEnum(t *testing.T) {
	uf := &UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"check-weird": {Target: "k8s", Disposable: new(true)},
		},
	}
	err := validateCheckBeds(uf)
	if err == nil || !strings.Contains(err.Error(), "unsupported target") {
		t.Fatalf("expected target-enum error, got %v", err)
	}
}

// TestValidateCheckBeds_VmRefMustResolve asserts a vm-target bed whose vm:
// entity is undefined is rejected, and that a defined entity passes.
func TestValidateCheckBeds_VmRefMustResolve(t *testing.T) {
	missing := &UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"check-k3s-vm": {Target: "vm", From: "k3s-vm", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(missing); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-vm-ref error, got %v", err)
	}
	ok := &UnifiedFile{
		VM: rawTemplateMap(map[string]*VmSpec{"k3s-vm": {}}),
		Bundle: map[string]spec.BundleNode{
			"check-k3s-vm": {Target: "vm", From: "k3s-vm", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(ok); err != nil {
		t.Fatalf("defined vm ref should pass, got %v", err)
	}
}

// TestValidateCheckBeds_LocalRefMustResolve asserts a local-target bed whose
// local: template is undefined is rejected, and that a defined one passes.
func TestValidateCheckBeds_LocalRefMustResolve(t *testing.T) {
	missing := &UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"check-local": {Target: "local", From: "check-local", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(missing); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-local-ref error, got %v", err)
	}
	ok := &UnifiedFile{
		Local: rawTemplateMap(map[string]*LocalSpec{"check-local": {}}),
		Bundle: map[string]spec.BundleNode{
			"check-local": {Target: "local", From: "check-local", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(ok); err != nil {
		t.Fatalf("defined local ref should pass, got %v", err)
	}
}

// TestPersistBedDeployOverrides_SeedsPortBeforeConfig pins the fix for the
// bug class where a kind:check pod bed's project-declared deploy-shaped fields
// (port:/volume:/env:/tunnel:) never reached the per-host deploy.yml: charly check
// run shelled out `charly bundle add`/`charly config` with just the bed NAME, and both
// source port/security/network from the IMAGE LABELS (gating port writes behind
// an operator -p), so the bed's `port: 45434:11434` remap silently fell back to
// the image default and collided with a same-image production deploy at start.
// persistBedDeployOverrides seeds the bed node's overrides up front so the
// existing charly config -> MergeDeployOntoMetadata -> quadlet path honors them.
func TestPersistBedDeployOverrides_SeedsPortBeforeConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-existing unrelated deploy must survive the seed (merge, not clobber).
	// Compact node-form: the port collection lives INLINE in the pod value.
	initialYAML := `version: 2026.203.2359
ollama:
    pod:
        image: ollama
        port:
            - 11434:11434
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// A bed whose key differs from its image and whose port remaps off the
	// image default — exactly the check-cachyos-ollama-pod shape.
	bed := spec.BundleNode{
		Target:     "pod",
		Image:      "ollama",
		Port:       []string{"45434:11434"},
		Disposable: new(true),
		Lifecycle:  "dev",
	}
	persistBedDeployOverrides("check-cachyos-ollama-pod", bed)

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after seed: %v", err)
	}
	entry, ok := dc.Bundle["check-cachyos-ollama-pod"]
	if !ok {
		t.Fatal("bed entry not seeded into deploy.yml")
	}
	if len(entry.Port) != 1 || entry.Port[0] != "45434:11434" {
		t.Errorf("bed port not seeded: got %v, want [45434:11434]", entry.Port)
	}
	if entry.Image != "ollama" || entry.Target != "pod" {
		t.Errorf("bed image/target not seeded: got image=%q target=%q", entry.Image, entry.Target)
	}
	if entry.Disposable == nil || !*entry.Disposable {
		t.Error("bed disposable not seeded (the check-runner requires it to authorize the unattended fresh-rebuild)")
	}
	// The sibling production deploy must be untouched (distinct key).
	sib, ok := dc.Bundle["ollama"]
	if !ok || len(sib.Port) != 1 || sib.Port[0] != "11434:11434" {
		t.Errorf("sibling 'ollama' deploy clobbered: got %+v", sib)
	}
}

// TestBedCheckLiveRefs proves `charly check run <bed>` check-lives the substrate AND
// every nested child (sorted, dotted) — so a nested pod's baked candy/box
// check runs against its real venue. Before the nested-check fix this produced
// only [name], so a nested selkies-kde pod was deployed but never evaluated.
func TestBedCheckLiveRefs(t *testing.T) {
	// Flat bed: just the substrate (identical to the prior behavior).
	if got := deploykit.BedCheckLiveRefs("check-pod", nil); len(got) != 1 || got[0] != "check-pod" {
		t.Fatalf("flat bed: got %v, want [check-pod]", got)
	}
	// Nested bed: substrate first, then each child as a sorted dotted path.
	nested := map[string]*spec.BundleNode{
		"selkies-kde": {Target: "pod"},
		"cuda-pod":    {Target: "pod"},
	}
	got := deploykit.BedCheckLiveRefs("check-cachyos-gpu-vm", nested)
	want := []string{
		"check-cachyos-gpu-vm",
		"check-cachyos-gpu-vm.cuda-pod", // sorted before selkies-kde
		"check-cachyos-gpu-vm.selkies-kde",
	}
	if len(got) != len(want) {
		t.Fatalf("nested bed: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("nested bed ref[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	// Android child: a target:android nested child shares the parent pod's
	// venue (its app-presence checks are baked into the parent's
	// android-emulator-layer and run in the parent ref) and has NO own venue
	// `charly check live` can resolve — so it gets NO dotted hop, while a pod sibling
	// still does. This is the check-coverage gate for the e740430 defect: a hop
	// for an android child wrongly resolved to a non-existent
	// `charly-<parent>.device` container, failing every nested pod→android bed's R10.
	androidNested := map[string]*spec.BundleNode{
		"web":    {Target: "pod"},
		"device": {Target: "android"},
	}
	// Stamp the descent traits (P9) exactly as the loader does — production passes
	// BedCheckLiveRefs children from the stamped tree; the android skip reads the venue trait.
	for _, c := range androidNested {
		kit.StampDescent(c, deployTraitsFor)
	}
	gotA := deploykit.BedCheckLiveRefs("check-android-emulator-pod", androidNested)
	wantA := []string{
		"check-android-emulator-pod",
		"check-android-emulator-pod.web", // pod child kept; android "device" omitted
	}
	if len(gotA) != len(wantA) {
		t.Fatalf("android bed: got %v, want %v (android child must be omitted)", gotA, wantA)
	}
	for i := range wantA {
		if gotA[i] != wantA[i] {
			t.Errorf("android bed ref[%d]: got %q, want %q", i, gotA[i], wantA[i])
		}
	}
}
