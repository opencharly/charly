package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestPrintDebugRetentionNotice asserts that a FAILED bed prints the
// target-appropriate inspect + destroy hints — the keep-the-bed-up-on-failure
// behavior (fail() no longer tears the bed down). Each target kind gets the
// right destroy command.
func TestPrintDebugRetentionNotice(t *testing.T) {
	cases := []struct {
		name     string
		node     BundleNode
		wantSubs []string
	}{
		{"pod", BundleNode{Target: "pod"}, []string{"left running for debugging", "podman exec charly-bed1", "charly remove bed1"}},
		{"vm", BundleNode{Target: "vm", From: "k3s-vm"}, []string{"VM \"k3s-vm\" left running", "charly vm destroy k3s-vm"}},
		{"local", BundleNode{Target: "local"}, []string{"local apply left in place", "charly remove bed1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printDebugRetentionNotice(&buf, "bed1", tc.node)
			got := buf.String()
			for _, sub := range tc.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("notice for %s missing %q; got:\n%s", tc.name, sub, got)
				}
			}
		})
	}
}

// TestExitCodeOf asserts exit-code extraction: nil→0, plain error→1,
// subprocess exit 2→2. Underpins the `charly check run <bed>` exit-code
// propagation (check-check failure = 2, infra failure = 1).
func TestExitCodeOf(t *testing.T) {
	if got := exitCodeOf(nil); got != 0 {
		t.Errorf("exitCodeOf(nil) = %d, want 0", got)
	}
	if got := exitCodeOf(errors.New("plain")); got != 1 {
		t.Errorf("exitCodeOf(plain) = %d, want 1", got)
	}
	if got := exitCodeOf(exec.Command("sh", "-c", "exit 2").Run()); got != 2 {
		t.Errorf("exitCodeOf(exit 2) = %d, want 2", got)
	}
}

// TestCheckFailedError asserts the distinct check-fail exit code is neither 0
// nor 1, and that a wrapped CheckFailedError is detectable via errors.As (the
// path main() uses to map it to CheckFailExitCode).
func TestCheckFailedError(t *testing.T) {
	if CheckFailExitCode == 0 || CheckFailExitCode == 1 {
		t.Fatalf("CheckFailExitCode must differ from 0 and 1, got %d", CheckFailExitCode)
	}
	var ef *CheckFailedError
	if !errors.As(fmt.Errorf("ctx: %w", &CheckFailedError{Failed: 3}), &ef) {
		t.Fatal("errors.As did not detect a wrapped CheckFailedError")
	}
	if ef.Failed != 3 {
		t.Errorf("Failed = %d, want 3", ef.Failed)
	}
	if got := (&CheckFailedError{Failed: 3}).Error(); got != "3 check(s) failed" {
		t.Errorf("Error() = %q, want \"3 check(s) failed\"", got)
	}
	if got := (&CheckFailedError{Msg: "bed x: check checks failed"}).Error(); got != "bed x: check checks failed" {
		t.Errorf("Error() msg override = %q", got)
	}
}

// TestCheckBeds_DerivesFromDisposableBundles asserts the R10 bed set is derived
// from the `disposable: true` bundles in the Deploy map (the separate kind:check
// block was removed — a bed IS a disposable bundle); a non-disposable deploy is
// NOT a bed.
func TestCheckBeds_DerivesFromDisposableBundles(t *testing.T) {
	uf := &UnifiedFile{
		Bundle: map[string]BundleNode{
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
		Bundle: map[string]BundleNode{
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
		Bundle: map[string]BundleNode{
			"check-k3s-vm": {Target: "vm", From: "k3s-vm", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(missing); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-vm-ref error, got %v", err)
	}
	ok := &UnifiedFile{
		VM: rawTemplateMap(map[string]*VmSpec{"k3s-vm": {}}),
		Bundle: map[string]BundleNode{
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
		Bundle: map[string]BundleNode{
			"check-local": {Target: "local", From: "check-local", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(missing); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-local-ref error, got %v", err)
	}
	ok := &UnifiedFile{
		Local: rawTemplateMap(map[string]*LocalSpec{"check-local": {}}),
		Bundle: map[string]BundleNode{
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
	initialYAML := `version: 2026.186.2323
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
	bed := BundleNode{
		Target:     "pod",
		Image:      "ollama",
		Port:       []string{"45434:11434"},
		Disposable: new(true),
		Lifecycle:  "dev",
	}
	persistBedDeployOverrides("check-cachyos-ollama-pod", bed)

	dc, err := LoadBundleConfig()
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
	if got := bedCheckLiveRefs("check-pod", nil); len(got) != 1 || got[0] != "check-pod" {
		t.Fatalf("flat bed: got %v, want [check-pod]", got)
	}
	// Nested bed: substrate first, then each child as a sorted dotted path.
	nested := map[string]*BundleNode{
		"selkies-kde": {Target: "pod"},
		"cuda-pod":    {Target: "pod"},
	}
	got := bedCheckLiveRefs("check-cachyos-gpu-vm", nested)
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
	androidNested := map[string]*BundleNode{
		"web":    {Target: "pod"},
		"device": {Target: "android"},
	}
	// Stamp the descent traits (P9) exactly as the loader does — production passes
	// BedCheckLiveRefs children from the stamped tree; the android skip reads the venue trait.
	for _, c := range androidNested {
		kit.StampDescent(c, deployTraitsFor)
	}
	gotA := bedCheckLiveRefs("check-android-emulator-pod", androidNested)
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
