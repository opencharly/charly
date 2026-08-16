package fleet

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// TestEphemeralFallbackNode_SeedsIdentityOnly is the regression test for the FINAL/K5 unit 6a
// bed-caught bug: a fresh (no prior overlay entry) ephemeral registration must seed Target/From
// from the authored node — a bare spec.FleetNode{} discriminates as "group" on reload and fails
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

// TestEnsureEphemeralFleetConfig_NilMapPanic is the regression test for the FINAL/K5 unit 6a
// RCA #5 live-probe-caught bug: persistEphemeralRuntime's `dc.Fleet[key] = node` write panicked
// ("assignment to entry in nil map") on a genuinely FRESH per-host overlay, whose loadFleetConfig
// result was a non-nil *deploykit.FleetConfig with a NIL Fleet field — a shape the old guard
// (`if dc == nil`) never covered. Every bed run hit this on first registration; the panic was
// silently swallowed somewhere upstream (a bed run reported PASS regardless) until an
// orchestrator-driven live probe surfaced it directly. This test proves ensureEphemeralFleetConfig
// makes a subsequent map write panic-free for every dc shape loadFleetConfig can return.
func TestEnsureEphemeralFleetConfig_NilMapPanic(t *testing.T) {
	cases := []struct {
		name string
		dc   *deploykit.FleetConfig
	}{
		{"nil *FleetConfig entirely", nil},
		{"non-nil *FleetConfig, nil Fleet field — the RCA #5 shape", &deploykit.FleetConfig{}},
		{"already-initialized Fleet map (no-op path)", &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{"existing": {}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ensureEphemeralFleetConfig(tc.dc)
			if got == nil {
				t.Fatal("ensureEphemeralFleetConfig() returned nil *FleetConfig")
			}
			if got.Fleet == nil {
				t.Fatal("ensureEphemeralFleetConfig() left Fleet nil")
			}
			// The actual regression: this write must NOT panic.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("write to dc.Fleet panicked: %v", r)
				}
			}()
			got.Fleet["probe-key"] = spec.FleetNode{Target: "vm"}
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
// illegal as a literal dc.Fleet map key (sdk/spec/deploy_tree_validate.go's ValidateDeploymentName rejects any
// '.'), so every ephemeral dc.Fleet accessor MUST key through this sanitized "vm:<domain-id>"
// form — the SAME scheme sdk/deploykit/vm_deploy_state.go's SaveVmDeployState already uses
// (matching sdk/vmshared.VmDomainIdentity's explicit "." -> "-" replacement) — never the raw
// deployName.
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
		{"undotted top-level name", "myapp", "charly-fleet-del-myapp"},
		{"dotted nested address — the RCA #4 shape", "check-sidecar-pod.check-sidecar-pod-ephvm", "charly-fleet-del-check-sidecar-pod-check-sidecar-pod-ephvm"},
		{"multi-level dotted address", "a.b.c", "charly-fleet-del-a-b-c"},
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
// depends on: the dc.Fleet KEY an entry lives under is the sanitized ephemeralOverlayKey, while
// EphemeralRuntime.DeployAddress (set alongside it in persistEphemeralRuntime) is the ORIGINAL
// possibly-dotted deployName — teardownChildrenRec's recursive `charly fleet del` call depends
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

// TestRegisterTransientTimerArgs_PinsWorkingDirectory is the regression test for the bed-robustness
// batch item 1 bug: the ephemeral teardown timer NEVER worked (~21 recorded failures over weeks)
// because the transient unit's ExecStart ran from the user systemd manager's default working
// directory (the user's home), not the project directory the deploy was registered from — so the
// self-exec'd `charly fleet del` could never find `charly.yml`. This test proves the constructed
// systemd-run argv carries `--working-directory=<wd>` set to the CALLER's resolved directory (never
// silently omitted, never defaulted to something else), and that argv ordering keeps the exe +
// del-argv intact.
func TestRegisterTransientTimerArgs_PinsWorkingDirectory(t *testing.T) {
	got := registerTransientTimerArgs(
		"charly-fleet-del-myapp-12345",
		30*time.Minute,
		"/home/user/projects/myproject",
		"/usr/local/bin/charly",
		"/tmp/charly-bed-cfg-x/charly.yml",
		[]string{"fleet", "del", "myapp", "--assume-yes"},
	)
	// Asserted as INVARIANTS, not as an exact argv. The whole-slice equality this replaces failed
	// the moment a flag was added even though every property it documents still held — an
	// over-specified assertion reports a regression where there is none, and the tempting response
	// is to update the expectation without checking which property broke. These four checks name
	// the properties instead, so a real regression is distinguishable from an addition.
	head := []string{"--user", "--unit=charly-fleet-del-myapp-12345", "--on-active=30m0s"}
	for i, w := range head {
		if i >= len(got) || got[i] != w {
			t.Errorf("registerTransientTimerArgs()[%d] = %q, want %q", i, got[i], w)
		}
	}
	// The exe and the del-argv must stay contiguous AND last: systemd-run treats the first
	// non-flag word as the command, so any flag appended AFTER the exe would be handed to
	// `charly fleet del` as an argument instead of to systemd-run.
	tail := []string{"/usr/local/bin/charly", "fleet", "del", "myapp", "--assume-yes"}
	if len(got) < len(tail) {
		t.Fatalf("registerTransientTimerArgs() = %v, too short for exe+del argv", got)
	}
	for i, w := range tail {
		if g := got[len(got)-len(tail)+i]; g != w {
			t.Errorf("tail[%d] = %q, want %q (exe + del argv must be contiguous and last)", i, g, w)
		}
	}
	// `fleet del` is not freshness-safe, so without this the reaper refuses to run whenever the
	// source tree is newer than the binary — the normal state on a developer host.
	if !slices.Contains(got, "--setenv=CHARLY_SKIP_FRESHNESS_CHECK=1") {
		t.Errorf("missing freshness bypass for the machine-invoked reaper; got %v", got)
	}
	// The registration's own deploy-config path must ride along, or a timer firing after a bed
	// session ends reads the operator overlay — which has no record of the entity, so it can
	// neither resolve it nor verify identity. This is what makes the state lifetime match the
	// timer lifetime.
	if !slices.Contains(got, "--setenv=CHARLY_DEPLOY_CONFIG=/tmp/charly-bed-cfg-x/charly.yml") {
		t.Errorf("missing deploy-config passthrough; got %v", got)
	}
	// Explicit assertion that the working-directory flag is present at all — the exact bug class
	// (a silently-omitted flag) that produced the "no charly.yml found in /home/<user>" failure.
	found := false
	for _, a := range got {
		if strings.HasPrefix(a, "--working-directory=") {
			found = true
			if a != "--working-directory=/home/user/projects/myproject" {
				t.Errorf("--working-directory flag = %q, want pinned to the caller's resolved wd", a)
			}
		}
	}
	if !found {
		t.Fatal("registerTransientTimerArgs() omitted --working-directory — the transient unit would fall back to the user systemd manager's default (home dir), reproducing the original bug")
	}
}

func TestEphemeralDeployDelArgv(t *testing.T) {
	got := spec.FleetDelArgv("myapp")
	want := []string{"fleet", "del", "myapp", "--assume-yes"}
	if len(got) != len(want) {
		t.Fatalf("spec.FleetDelArgv() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("spec.FleetDelArgv()[%d] = %q, want %q", i, got[i], want[i])
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

// TestEphemeralByIDFromFleetConfig covers the pure scan lookupEphemeralByID applies once it has
// an already-loaded FleetConfig — the reverse-channel-coupled LOAD itself (loadFleetConfig, the
// loaderkit overlay read) needs a live reverse channel and is not unit-testable standalone
// (mirrors candy/plugin-pod/remove_orchestration.go's sidecarNamesFromFleetConfig split).
func TestEphemeralByIDFromFleetConfig(t *testing.T) {
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"parent-vm": {VmState: &spec.VmDeployState{Ephemeral: &spec.EphemeralRuntime{
			ID:          "abc123",
			TtlDeadline: time.Now().Add(time.Hour).Format(time.RFC3339),
		}}},
		"other": {},
	}}

	got, err := ephemeralByIDFromFleetConfig(dc, "abc123")
	if err != nil {
		t.Fatalf("ephemeralByIDFromFleetConfig() error = %v", err)
	}
	if got.ID != "abc123" {
		t.Errorf("ephemeralByIDFromFleetConfig() ID = %q, want abc123", got.ID)
	}

	if _, err := ephemeralByIDFromFleetConfig(dc, "does-not-exist"); err == nil {
		t.Error("ephemeralByIDFromFleetConfig() with unknown id: want error, got nil")
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

// TestReaperDelArgv_UniformVmForm asserts the reaper's argv: the "vm:" address form for EVERY
// registration, plus the incarnation token as a FLAG.
//
// The prefix is what lets the reaper resolve with no project tree — the worktree it registered from
// is routinely deleted once its PR lands. It used to be unsafe for registrations whose state does
// not outlive their session, because resolveDelNode's fallback synthesises a node from the address
// alone; that is now handled by verifying identity BEFORE resolution (timerDrivenDelRefusal), so
// the form is uniform.
//
// The token is a FLAG, never an environment variable: the guarantee is enforced by the binary that
// RUNS, and a binary predating the check would silently ignore an env var and reap with no
// incarnation check at all. An unknown flag is a parse error instead — measured on an installed
// 2026.223.1347 binary, `fleet del … --require-timer-unit=x` exits 80 without entering the command
// body, while the same command without it parses and proceeds.
func TestReaperDelArgv_UniformVmForm(t *testing.T) {
	got := reaperDelArgv("bed.ephvm", "charly-fleet-del-bed-ephvm-1786830381")
	if !slices.Contains(got, "vm:bed.ephvm") {
		t.Errorf("reaper argv must use the vm: form so it resolves without a project; got %v", got)
	}
	if !slices.Contains(got, "--require-timer-unit=charly-fleet-del-bed-ephvm-1786830381") {
		t.Errorf("incarnation token must travel as a FLAG (fails closed on an old binary); got %v", got)
	}
	for _, a := range got {
		if len(a) > 8 && a[:8] == "--setenv" {
			t.Errorf("reaper argv must carry no setenv token — two channels means the weakest defines the guarantee; got %v", got)
		}
	}
}
