package bundle

import (
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// TestEphemeralFallbackNode_SeedsIdentityOnly is the regression test for the FINAL/K5 unit 6a
// bed-caught bug: a fresh (no prior overlay entry) ephemeral registration must seed Target/From
// from the authored node — a bare spec.BundleNode{} discriminates as "group" on reload and fails
// #GroupInput's closed schema on the leftover vm_state field. Structure fields (Children/Members)
// must NOT be copied — an overlay entry is state, never structure.
func TestEphemeralFallbackNode_SeedsIdentityOnly(t *testing.T) {
	authored := &spec.Deploy{
		Target:   "vm",
		From:     "eval-vm",
		Children: map[string]*spec.Deploy{"child": {}},
		Members:  map[string]*spec.Deploy{"peer": {}},
	}
	got := ephemeralFallbackNode(authored)
	if got.Target != "vm" {
		t.Errorf("Target = %q, want %q", got.Target, "vm")
	}
	if got.From != "eval-vm" {
		t.Errorf("From = %q, want %q", got.From, "eval-vm")
	}
	if got.Children != nil {
		t.Errorf("Children = %v, want nil (overlay entry is state, not structure)", got.Children)
	}
	if got.Members != nil {
		t.Errorf("Members = %v, want nil (overlay entry is state, not structure)", got.Members)
	}
}

// TestEphemeralFallbackNode_NilAuthored covers the defensive nil case (should never happen in
// practice — registerEphemeral already rejects a nil node before reaching this point — but the
// function must not panic).
func TestEphemeralFallbackNode_NilAuthored(t *testing.T) {
	got := ephemeralFallbackNode(nil)
	if got.Target != "" || got.From != "" {
		t.Errorf("ephemeralFallbackNode(nil) = %+v, want zero value", got)
	}
}

// TestEnsureEphemeralBundleConfig_NilMapPanic is the regression test for the FINAL/K5 unit 6a
// RCA #5 live-probe-caught bug: persistEphemeralRuntime's `dc.Bundle[key] = node` write panicked
// ("assignment to entry in nil map") on a genuinely FRESH per-host overlay, whose loadBundleConfig
// result was a non-nil *deploykit.BundleConfig with a NIL Bundle field — a shape the old guard
// (`if dc == nil`) never covered. Every bed run hit this on first registration; the panic was
// silently swallowed somewhere upstream (a bed run reported PASS regardless) until an
// orchestrator-driven live probe surfaced it directly. This test proves ensureEphemeralBundleConfig
// makes a subsequent map write panic-free for every dc shape loadBundleConfig can return.
func TestEnsureEphemeralBundleConfig_NilMapPanic(t *testing.T) {
	cases := []struct {
		name string
		dc   *deploykit.BundleConfig
	}{
		{"nil *BundleConfig entirely", nil},
		{"non-nil *BundleConfig, nil Bundle field — the RCA #5 shape", &deploykit.BundleConfig{}},
		{"already-initialized Bundle map (no-op path)", &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{"existing": {}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ensureEphemeralBundleConfig(tc.dc)
			if got == nil {
				t.Fatal("ensureEphemeralBundleConfig() returned nil *BundleConfig")
			}
			if got.Bundle == nil {
				t.Fatal("ensureEphemeralBundleConfig() left Bundle nil")
			}
			// The actual regression: this write must NOT panic.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("write to dc.Bundle panicked: %v", r)
				}
			}()
			got.Bundle["probe-key"] = spec.BundleNode{Target: "vm"}
		})
	}
}

// TestRecoverEphemeralOpPanic is the regression test for the FINAL/K5 unit 6a RCA #5 finding #2:
// a recovered panic must become a LOUD, sdk.EphemeralPanicMarker-tagged error — never silently
// vanish or crash the host process. Directly exercises recoverEphemeralOpPanic (command.go),
// which runEphemeralRegister/runEphemeralTeardown defer at their outermost entry point.
func TestRecoverEphemeralOpPanic(t *testing.T) {
	t.Run("no panic leaves errOut untouched", func(t *testing.T) {
		var errOut error
		func() {
			defer recoverEphemeralOpPanic(&errOut)
		}()
		if errOut != nil {
			t.Errorf("errOut = %v, want nil (no panic occurred)", errOut)
		}
	})
	t.Run("a panic is converted to a marked error, not re-panicked", func(t *testing.T) {
		var errOut error
		func() {
			defer recoverEphemeralOpPanic(&errOut)
			panic("assignment to entry in nil map")
		}()
		if errOut == nil {
			t.Fatal("errOut = nil, want a non-nil error after a recovered panic")
		}
		if !strings.Contains(errOut.Error(), sdk.EphemeralPanicMarker) {
			t.Errorf("errOut = %q, want it to contain the marker %q", errOut.Error(), sdk.EphemeralPanicMarker)
		}
		if !strings.Contains(errOut.Error(), "assignment to entry in nil map") {
			t.Errorf("errOut = %q, want it to preserve the original panic message", errOut.Error())
		}
	})
}

// TestEphemeralOverlayKey is the regression test for the FINAL/K5 unit 6a RCA #2 bed-caught bug:
// a nested deploy's dotted CLI address (e.g. "check-sidecar-pod.check-sidecar-pod-ephvm") is
// illegal as a literal dc.Bundle map key (charly/unified.go's validateDeploymentName rejects any
// '.'), so every ephemeral dc.Bundle accessor MUST key through this sanitized "vm:<domain-id>"
// form — the SAME scheme charly/vm_deploy_state.go's saveVmDeployState already uses (matching
// sdk/vmshared.VmDomainIdentity's explicit "." -> "-" replacement) — never the raw deployName.
func TestEphemeralOverlayKey(t *testing.T) {
	cases := []struct {
		name       string
		deployName string
		want       string
	}{
		{"undotted name unchanged (just prefixed)", "myapp", "vm:myapp"},
		{"dotted nested address sanitized", "check-sidecar-pod.check-sidecar-pod-ephvm", "vm:check-sidecar-pod-check-sidecar-pod-ephvm"},
		{"multi-level dotted address sanitized", "a.b.c", "vm:a-b-c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ephemeralOverlayKey(tc.deployName); got != tc.want {
				t.Errorf("ephemeralOverlayKey(%q) = %q, want %q", tc.deployName, got, tc.want)
			}
		})
	}
}

// TestEphemeralTimerUnitPrefix_UsesFullDottedPath is the regression test for the FINAL/K5 unit 6a
// RCA #4 bed-caught bug: a bed-4 check-live run FAILED at ephemeral-register-roundtrip's systemd-
// timer conjunct — but a live-system check (leftover systemd timers from the SAME run) PROVED
// registration actually fired correctly; the real bug was the check assertion's grep pattern using
// the LEAF deploy name ("check-sidecar-pod-ephvm") instead of the FULL DOTTED PATH
// registerTransientTimer actually sanitizes into the unit name
// ("check-sidecar-pod.check-sidecar-pod-ephvm" -> "check-sidecar-pod-check-sidecar-pod-ephvm").
// This test guards the naming FORMULA itself so a future caller (a check assertion, an operator
// script) has something to verify its grep pattern against.
func TestEphemeralTimerUnitPrefix_UsesFullDottedPath(t *testing.T) {
	cases := []struct {
		name       string
		deployName string
		want       string
	}{
		{"undotted top-level name", "myapp", "charly-bundle-del-myapp"},
		{"dotted nested address — the RCA #4 shape", "check-sidecar-pod.check-sidecar-pod-ephvm", "charly-bundle-del-check-sidecar-pod-check-sidecar-pod-ephvm"},
		{"multi-level dotted address", "a.b.c", "charly-bundle-del-a-b-c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ephemeralTimerUnitPrefix(tc.deployName); got != tc.want {
				t.Errorf("ephemeralTimerUnitPrefix(%q) = %q, want %q", tc.deployName, got, tc.want)
			}
		})
	}
}

// TestEphemeralOverlayKey_DistinctFromDeployAddress documents the split contract the RCA #2 fix
// depends on: the dc.Bundle KEY an entry lives under is the sanitized ephemeralOverlayKey, while
// EphemeralRuntime.DeployAddress (set alongside it in persistEphemeralRuntime) is the ORIGINAL
// possibly-dotted deployName — teardownChildrenRec's recursive `charly bundle del` call depends
// on recovering exactly that original address, since the sanitized key itself is not reversible
// (VmDomainIdentity's "." -> "-" replacement is lossy).
func TestEphemeralOverlayKey_DistinctFromDeployAddress(t *testing.T) {
	deployName := "check-sidecar-pod.check-sidecar-pod-ephvm"
	key := ephemeralOverlayKey(deployName)
	if key == deployName {
		t.Fatalf("ephemeralOverlayKey(%q) = %q, want a distinct sanitized form", deployName, key)
	}
	runtime := &spec.EphemeralRuntime{DeployAddress: deployName}
	if runtime.DeployAddress != deployName {
		t.Errorf("DeployAddress = %q, want the original dotted deployName %q", runtime.DeployAddress, deployName)
	}
	if runtime.DeployAddress == key {
		t.Errorf("DeployAddress must stay the ORIGINAL address, not the sanitized map key %q", key)
	}
}

func TestDescentVenue(t *testing.T) {
	cases := []struct {
		name string
		node *spec.Deploy
		want string
	}{
		{"nil node", nil, ""},
		{"nil descent", &spec.Deploy{}, ""},
		{"ssh venue", &spec.Deploy{Descent: &spec.DescentDescriptor{Venue: "ssh"}}, "ssh"},
		{"container venue", &spec.Deploy{Descent: &spec.DescentDescriptor{Venue: "container"}}, "container"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := descentVenue(tc.node); got != tc.want {
				t.Errorf("descentVenue() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEphemeralDeployDelArgv(t *testing.T) {
	got := ephemeralDeployDelArgv("myapp")
	want := []string{"bundle", "del", "myapp", "--assume-yes"}
	if len(got) != len(want) {
		t.Fatalf("ephemeralDeployDelArgv() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ephemeralDeployDelArgv()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestEffectiveEphemeralTTL_NoParent(t *testing.T) {
	node := &spec.Deploy{Ephemeral: &spec.EphemeralLifetime{TTL: "30m"}}
	got, err := effectiveEphemeralTTL(node, "")
	if err != nil {
		t.Fatalf("effectiveEphemeralTTL() error = %v", err)
	}
	if got != 30*time.Minute {
		t.Errorf("effectiveEphemeralTTL() = %v, want 30m", got)
	}
}

func TestEffectiveEphemeralTTL_Default(t *testing.T) {
	node := &spec.Deploy{Ephemeral: &spec.EphemeralLifetime{}}
	got, err := effectiveEphemeralTTL(node, "")
	if err != nil {
		t.Fatalf("effectiveEphemeralTTL() error = %v", err)
	}
	if got != time.Hour {
		t.Errorf("effectiveEphemeralTTL() = %v, want 1h default", got)
	}
}

// TestEphemeralByIDFromBundleConfig covers the pure scan lookupEphemeralByID applies once it has
// an already-loaded BundleConfig — the seam-coupled LOAD itself (loadBundleConfig, over
// "pod-config-load-bundle") needs a live reverse channel and is not unit-testable standalone
// (mirrors candy/plugin-pod/remove_orchestration.go's sidecarNamesFromBundleConfig split).
func TestEphemeralByIDFromBundleConfig(t *testing.T) {
	dc := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		"parent-vm": {VmState: &spec.VmDeployState{Ephemeral: &spec.EphemeralRuntime{
			ID:          "abc123",
			TtlDeadline: time.Now().Add(time.Hour).Format(time.RFC3339),
		}}},
		"other": {},
	}}

	got, err := ephemeralByIDFromBundleConfig(dc, "abc123")
	if err != nil {
		t.Fatalf("ephemeralByIDFromBundleConfig() error = %v", err)
	}
	if got.ID != "abc123" {
		t.Errorf("ephemeralByIDFromBundleConfig() ID = %q, want abc123", got.ID)
	}

	if _, err := ephemeralByIDFromBundleConfig(dc, "does-not-exist"); err == nil {
		t.Error("ephemeralByIDFromBundleConfig() with unknown id: want error, got nil")
	}
}

// TestClipTTLToParent covers the pure TTL-clipping math effectiveEphemeralTTL applies once it has
// a resolved parent EphemeralRuntime — the parent LOOKUP itself is seam-coupled (not
// unit-testable standalone; covered by the check-sidecar-pod ephemeral bed extension instead).
func TestClipTTLToParent(t *testing.T) {
	t.Run("no deadline is a no-op", func(t *testing.T) {
		got, err := clipTTLToParent(time.Hour, "p1", &spec.EphemeralRuntime{})
		if err != nil || got != time.Hour {
			t.Errorf("clipTTLToParent() = (%v, %v), want (1h, nil)", got, err)
		}
	})
	t.Run("clips to parent remaining", func(t *testing.T) {
		parent := &spec.EphemeralRuntime{TtlDeadline: time.Now().Add(5 * time.Minute).Format(time.RFC3339)}
		got, err := clipTTLToParent(time.Hour, "p1", parent)
		if err != nil {
			t.Fatalf("clipTTLToParent() error = %v", err)
		}
		if got <= 0 || got > 5*time.Minute {
			t.Errorf("clipTTLToParent() = %v, want clipped to ~5m", got)
		}
	})
	t.Run("declared under remaining stands", func(t *testing.T) {
		parent := &spec.EphemeralRuntime{TtlDeadline: time.Now().Add(time.Hour).Format(time.RFC3339)}
		got, err := clipTTLToParent(5*time.Minute, "p1", parent)
		if err != nil || got != 5*time.Minute {
			t.Errorf("clipTTLToParent() = (%v, %v), want (5m, nil)", got, err)
		}
	})
	t.Run("expired parent errors", func(t *testing.T) {
		parent := &spec.EphemeralRuntime{TtlDeadline: time.Now().Add(-time.Minute).Format(time.RFC3339)}
		if _, err := clipTTLToParent(time.Hour, "p1", parent); err == nil {
			t.Error("clipTTLToParent() with expired parent: want error, got nil")
		}
	})
	t.Run("unparseable deadline is a no-op", func(t *testing.T) {
		parent := &spec.EphemeralRuntime{TtlDeadline: "not-a-time"}
		got, err := clipTTLToParent(time.Hour, "p1", parent)
		if err != nil || got != time.Hour {
			t.Errorf("clipTTLToParent() = (%v, %v), want (1h, nil)", got, err)
		}
	})
}
