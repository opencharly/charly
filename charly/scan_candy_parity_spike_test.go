package main

// scan_candy_parity_spike_test.go — the W9 RDD SPIKE proving loaderkit.ScanCandyManifest produces
// byte-identical (CandyModel, CandyView) output to the pre-move scanCandy+populateCandyFromYAML+
// projectCandyModel/projectCandyView pipeline, on real candies exercising the three derived-logic
// branches (bake_plugin→require, package-section derivation, port normalization) plus a negative
// (malformed-manifest) case. THROWAWAY per RDD — deleted once the port lands; the knowledge (byte
// parity confirmed) is what carries into the plan, not this file.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
)

func TestScanCandyParitySpike(t *testing.T) {
	cases := []string{
		"../candy/charly-mcp",             // bake_plugin -> require implication
		"../candy/charly",                 // distro:/package: derivation (tag + format sections)
		"../candy/android-emulator-layer", // port normalization (plain-int form)
	}
	for _, dir := range cases {
		t.Run(dir, func(t *testing.T) {
			old, err := scanCandy(dir, "spike", UnifiedFileName)
			if err != nil {
				t.Fatalf("old scanCandy: %v", err)
			}
			oldModel := projectCandyModel(old)
			oldView := projectCandyView(old)

			newModel, newView, newRefs, err := loaderkit.ScanCandyManifest(dir, "spike", UnifiedFileName, parseCandyYAML)
			if err != nil {
				t.Fatalf("new loaderkit.ScanCandyManifest: %v", err)
			}

			// The host-side second pass ScanCandyManifest's doc comment describes: RunOps (registry-
			// adjacent opInContext/VerbCatalog, task #39, still core) and HasInit
			// (PopulateCandyInitSystem's cross-candy InitConfig resolution) are NOT scan-computable
			// — simulate that pass here exactly as the real ScanAllCandy wrapper will, then re-OR
			// the two predicates the same way ScanCandyManifest's own partial computation expects.
			newModel.RunOps = old.runOps()
			newView.HasInit = old.HasAnyInit()
			newModel.HasInstallFiles = newModel.HasInstallFiles || len(newModel.RunOps) > 0
			newModel.HasContent = newModel.HasContent || newModel.HasInstallFiles || newView.HasInit

			// Simulate the host's finalize step. QualifyRemoteSiblingDeps is DELIBERATELY not
			// called here: these are all LOCALLY-scanned candy dirs, and the pre-move
			// qualifyRemoteSiblingDeps is only ever invoked from the ScanRemoteCandy path (a
			// freshly-fetched remote candy) — calling it here would qualify every ref against an
			// empty RepoPath/SubPathPrefix and corrupt .Resolved, which is not what the real
			// pipeline does for a local candy. FinalizeCandyRefs alone (no qualification) proves
			// Finding 4's fix (CandyRefs carrying the rich form through to a LATER finalize,
			// instead of bare-stringing at scan time) reproduces the old byte-exact LOCAL result.
			loaderkit.FinalizeCandyRefs(&newModel, &newView, newRefs)

			// BakePlugin is skipped in the CandyModel diff below: the pre-move projectCandyModel
			// NEVER populated it (Finding 3 — a genuine schema gap, not a parity target), so OLD is
			// always empty here regardless of the candy's own bake_plugin: declaration. Assert the
			// NEW value directly instead — it must equal the bare-string projection of the rich
			// BakePlugin refs FinalizeCandyRefs just wrote.
			if want := bareRefs(old.BakePlugin); !reflect.DeepEqual(want, newModel.BakePlugin) {
				t.Errorf("CandyModel.BakePlugin: want bare projection %+v, got %+v", want, newModel.BakePlugin)
			}
			diffFields(t, "CandyModel", dir, oldModel, newModel, "BakePlugin")
			diffFields(t, "CandyView", dir, oldView, newView)

			// Parity on the RICH pre-qualification form itself (the carrier Finding 4 introduced) —
			// against the live *Candy's own Require/IncludedCandy/BakePlugin fields, which pre-move
			// hold the SAME rich CandyRefEntry shape (deploykit.CandyRef is a passthrough alias).
			if !reflect.DeepEqual(old.Require, newRefs.Require) {
				t.Errorf("CandyRefs.Require mismatch for %s:\nOLD: %+v\nNEW: %+v", dir, old.Require, newRefs.Require)
			}
			if !reflect.DeepEqual(old.IncludedCandy, newRefs.IncludedCandy) {
				t.Errorf("CandyRefs.IncludedCandy mismatch for %s:\nOLD: %+v\nNEW: %+v", dir, old.IncludedCandy, newRefs.IncludedCandy)
			}
			if !reflect.DeepEqual(old.BakePlugin, newRefs.BakePlugin) {
				t.Errorf("CandyRefs.BakePlugin mismatch for %s:\nOLD: %+v\nNEW: %+v", dir, old.BakePlugin, newRefs.BakePlugin)
			}
		})
	}
}

// TestScanCandyParitySpikePortProtocol is the synthetic fixture for the "udp:port" protocol-
// suffixed port form — no real candy in this repo currently authors one, so this constructs the
// minimal manifest that exercises populateCandyFromYAML's protocol-suffix normalization branch
// (fmt.Sprintf("%d/udp", p.Port) vs the bare "%d" form).
func TestScanCandyParitySpikePortProtocol(t *testing.T) {
	dir := t.TempDir()
	manifest := "candy:\n  version: 2026.001.0000\n  description: spike fixture\n  port:\n    - \"udp:5353\"\n    - 8080\n"
	if err := os.WriteFile(dir+"/"+UnifiedFileName, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	old, err := scanCandy(dir, "spike", UnifiedFileName)
	if err != nil {
		t.Fatalf("old scanCandy: %v", err)
	}
	oldModel := projectCandyModel(old)
	oldView := projectCandyView(old)

	newModel, newView, newRefs, err := loaderkit.ScanCandyManifest(dir, "spike", UnifiedFileName, parseCandyYAML)
	if err != nil {
		t.Fatalf("new loaderkit.ScanCandyManifest: %v", err)
	}
	newModel.RunOps = old.runOps()
	newView.HasInit = old.HasAnyInit()
	newModel.HasInstallFiles = newModel.HasInstallFiles || len(newModel.RunOps) > 0
	newModel.HasContent = newModel.HasContent || newModel.HasInstallFiles || newView.HasInit
	loaderkit.FinalizeCandyRefs(&newModel, &newView, newRefs)

	diffFields(t, "CandyModel", dir, oldModel, newModel)
	diffFields(t, "CandyView", dir, oldView, newView)

	// Assert the actual normalized form landed, not just that old==new (a shared bug would pass
	// the diff above but still be wrong).
	if len(newModel.Port) != 2 || newModel.Port[0].Protocol != "udp" || newModel.Port[0].Port != 5353 {
		t.Errorf("expected udp:5353 normalized PortSpec, got %+v", newModel.Port)
	}
}

// TestScanRemoteCandyParitySpike is the W9 spike for Finding 5: ScanRemoteCandy's post-scan
// mutation of Remote/RepoPath/SubPathPrefix, plus the sibling-dep qualification it triggers.
// Constructs a synthetic 2-candy "remote repo" (charly-mcp requiring the plain-name sibling
// plugin-mcp, also declared as its bake_plugin:) since no in-repo remote fixture exists, and
// compares the pre-move charly/layers.go ScanRemoteCandy against loaderkit.ScanRemoteCandy.
func TestScanRemoteCandyParitySpike(t *testing.T) {
	repoDir := t.TempDir()
	const repoPath = "github.com/example/testrepo"
	mcpDir := filepath.Join(repoDir, "candy", "charly-mcp")
	pluginDir := filepath.Join(repoDir, "candy", "plugin-mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Named node-form (`<name>: candy: {...}`) — every REAL candy manifest in this repo uses this
	// shape (never the bare kind-keyed `candy:` top-level form the OTHER spike fixtures use), and
	// it matters here: the bare form's typo-guard (rejectUnknownCandyTopLevelKeys /
	// candyYAMLKnownFields) is missing "bake_plugin" from its allowlist — a real, currently-shipping
	// gap in that dead-in-practice code path (reported to team-lead as a separable, non-blocking
	// finding for its own batch, NOT fixed here — R2's separability test: this cutover's own claim
	// is provable without it, since no real candy hits that path). Using the named form here avoids
	// exercising that unrelated bug while still proving THIS cutover's actual target: the
	// Remote/RepoPath/SubPathPrefix mutation + sibling-dep qualification (Finding 5).
	mcpManifest := "charly-mcp:\n    candy:\n        version: 2026.001.0000\n        description: spike fixture\n        require:\n            - plugin-mcp\n        bake_plugin:\n            - plugin-mcp\n"
	pluginManifest := "plugin-mcp:\n    candy:\n        version: 2026.001.0000\n        description: spike fixture\n"
	if err := os.WriteFile(filepath.Join(mcpDir, UnifiedFileName), []byte(mcpManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, UnifiedFileName), []byte(pluginManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	wantRefs := map[string]bool{
		repoPath + "/candy/charly-mcp": true,
		repoPath + "/candy/plugin-mcp": true,
	}

	oldLayers, err := ScanRemoteCandy(repoDir, repoPath, wantRefs)
	if err != nil {
		t.Fatalf("old ScanRemoteCandy: %v", err)
	}
	newScanned, err := loaderkit.ScanRemoteCandy(repoDir, repoPath, wantRefs, parseCandyYAML)
	if err != nil {
		t.Fatalf("new loaderkit.ScanRemoteCandy: %v", err)
	}

	if len(oldLayers) != len(newScanned) {
		t.Fatalf("result count mismatch: OLD %d, NEW %d", len(oldLayers), len(newScanned))
	}

	for ref, old := range oldLayers {
		sc, ok := newScanned[ref]
		if !ok {
			t.Errorf("ref %s: missing from new result", ref)
			continue
		}
		t.Run(ref, func(t *testing.T) {
			oldModel := projectCandyModel(old)
			oldView := projectCandyView(old)
			newModel, newView, newRefs := sc.Model, sc.View, sc.Refs

			// Same host-side second pass every other spike case simulates (RunOps/HasInit are not
			// scan-computable — see TestScanCandyParitySpike's doc comment).
			newModel.RunOps = old.runOps()
			newView.HasInit = old.HasAnyInit()
			newModel.HasInstallFiles = newModel.HasInstallFiles || len(newModel.RunOps) > 0
			newModel.HasContent = newModel.HasContent || newModel.HasInstallFiles || newView.HasInit

			// loaderkit.ScanRemoteCandy already ran QualifyRemoteSiblingDeps internally (unlike the
			// plain ScanCandyManifest spike above) — only FinalizeCandyRefs remains to reach the FINAL
			// bare-string form.
			loaderkit.FinalizeCandyRefs(&newModel, &newView, newRefs)

			if want := bareRefs(old.BakePlugin); !reflect.DeepEqual(want, newModel.BakePlugin) {
				t.Errorf("CandyModel.BakePlugin: want bare projection %+v, got %+v", want, newModel.BakePlugin)
			}
			diffFields(t, "CandyModel", ref, oldModel, newModel, "BakePlugin")
			diffFields(t, "CandyView", ref, oldView, newView)

			// The Finding-5 assertion itself: Remote/RepoPath/SubPathPrefix landed identically.
			if oldView.Remote != newView.Remote || oldView.RepoPath != newView.RepoPath || oldView.SubPathPrefix != newView.SubPathPrefix {
				t.Errorf("remote metadata mismatch for %s:\nOLD: Remote=%v RepoPath=%q SubPathPrefix=%q\nNEW: Remote=%v RepoPath=%q SubPathPrefix=%q",
					ref, oldView.Remote, oldView.RepoPath, oldView.SubPathPrefix, newView.Remote, newView.RepoPath, newView.SubPathPrefix)
			}

			// Parity on the RICH post-qualification form (the .Resolved qualification itself —
			// Finding 4+5 combined: this is the ONE path that actually exercises
			// qualifyRemoteSiblingDeps/.Resolved, unlike the local-only cases above).
			if !reflect.DeepEqual(old.Require, newRefs.Require) {
				t.Errorf("CandyRefs.Require mismatch for %s:\nOLD: %+v\nNEW: %+v", ref, old.Require, newRefs.Require)
			}
			if !reflect.DeepEqual(old.IncludedCandy, newRefs.IncludedCandy) {
				t.Errorf("CandyRefs.IncludedCandy mismatch for %s:\nOLD: %+v\nNEW: %+v", ref, old.IncludedCandy, newRefs.IncludedCandy)
			}
			if !reflect.DeepEqual(old.BakePlugin, newRefs.BakePlugin) {
				t.Errorf("CandyRefs.BakePlugin mismatch for %s:\nOLD: %+v\nNEW: %+v", ref, old.BakePlugin, newRefs.BakePlugin)
			}
		})
	}

	// Assert the qualification actually fired (a shared bug in both old+new could pass every diff
	// above while both sides skip qualification entirely).
	mcpRef := repoPath + "/candy/charly-mcp"
	wantResolved := repoPath + "/candy/plugin-mcp"
	if got := oldLayers[mcpRef].Require[0].Resolved; got != wantResolved {
		t.Errorf("OLD sanity check: charly-mcp's require[0].Resolved = %q, want %q", got, wantResolved)
	}
	if got := newScanned[mcpRef].Refs.Require[0].Resolved; got != wantResolved {
		t.Errorf("NEW sanity check: charly-mcp's require[0].Resolved = %q, want %q", got, wantResolved)
	}
}

// diffFields reports ONLY the top-level struct fields that differ between old and new, by name
// and value — a whole-struct %+v dump is too noisy for these deeply-nested candy views. skip
// names fields the caller has already asserted separately (a deliberate old/new divergence, e.g.
// a genuine pre-move schema gap being fixed by this move — never a silent parity waiver).
func diffFields(t *testing.T, label, dir string, oldV, newV any, skip ...string) {
	t.Helper()
	skipSet := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipSet[s] = true
	}
	ov := reflect.ValueOf(oldV)
	nv := reflect.ValueOf(newV)
	ot := ov.Type()
	for i := 0; i < ot.NumField(); i++ {
		if skipSet[ot.Field(i).Name] {
			continue
		}
		of := ov.Field(i).Interface()
		nf := nv.Field(i).Interface()
		if !reflect.DeepEqual(of, nf) {
			t.Errorf("%s.%s mismatch for %s:\nOLD: %+v\nNEW: %+v", label, ot.Field(i).Name, dir, of, nf)
		}
	}
}

// TestScanCandyParitySpikeMalformed is the required negative case: a candy directory whose
// manifest fails to parse must fail IDENTICALLY (same error text) through both paths.
func TestScanCandyParitySpikeMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/"+UnifiedFileName, []byte("candy:\n  version: [this is not valid\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, oldErr := scanCandy(dir, "spike", UnifiedFileName)
	if oldErr == nil {
		t.Fatal("expected old scanCandy to fail on malformed YAML")
	}

	_, _, _, newErr := loaderkit.ScanCandyManifest(dir, "spike", UnifiedFileName, parseCandyYAML)
	if newErr == nil {
		t.Fatal("expected new loaderkit.ScanCandyManifest to fail on malformed YAML")
	}

	if oldErr.Error() != newErr.Error() {
		t.Errorf("error text mismatch:\nOLD: %v\nNEW: %v", oldErr, newErr)
	}
}
