package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// ephemeral.go — the FINAL/K5 unit-6a move of charly/ephemeral_lifecycle.go: cross-substrate
// ephemeral-deploy lifecycle (systemd TTL transient timer, parent/child nesting, vm-snapshot
// refcounts, charly.yml persistence). command:fleet is the substrate-neutral deploy-lifecycle
// owner: this body is written substrate-agnostic (no vm/pod/kubernetes branch of its own), reached via
// the SAME OpEphemeralRegister/OpEphemeralTeardown legs regardless of which substrate calls
// them. **Only the VM substrate actually calls them TODAY** (candy/plugin-deploy-vm's
// dispatchVmEphemeralRegister / dispatchVmEphemeralTeardown) — pod and kubernetes Add/Del never reach this code
// (verified by call-graph, not the deleted charly/ephemeral_lifecycle.go's own header, which
// falsely claimed "all three target types... call into these functions" — an R1 false-comment
// instance this move does NOT repeat). Wiring pod/kubernetes's Add/Del paths to call it too is tracked
// as its own bed-robustness-batch item (their dispatch lives in candy/plugin-deploy-pod /
// candy/plugin-pod, outside this unit's scope); `ephemeral: true` on a pod/kubernetes deploy is
// rejected at load time in the meantime (charly/validate_ephemeral.go) rather than silently
// no-op'd. Config persistence: reads route through the cycle-free loaderkit.LoadHostFleetConfigViaExecutor
// read (loadFleetConfig below); writes run PLUGIN-SIDE via deploykit.SaveFleetConfig
// (saveDeployConfig, config_cmd.go — #55 K4 config-write seam-collapse, no host deploy-config-save
// seam). Calling deploykit.LoadFleetConfig() directly — relying on the
// compiled-in placement's shared process-wide deploykit.DeployStateHost var — is the
// placement-dependent silent-degradation anti-pattern this program has already fixed twice
// (candy/plugin-pod's resolveSidecarNames + engine-resolution, remove_orchestration.go): correct
// only because command:fleet happens to be compiled-in TODAY, a per-BUILD fact never an authoring
// guarantee (the dual-placement Key Rule) — silently empty out-of-process instead of the loud
// HostBuild-transport error the seam gives. This also fixes the deploy_file.go:99 silent-nil
// footgun for BOTH placements uniformly, not just the compiled-in one.
//
// The vm-snapshot refcount calls (vmshared.Increment/DecrementSnapshotRefcount) are
// ALREADY sdk-portable (sdk/vmshared) — reached directly, no alias, no seam. The systemd
// self-exec half (registerTransientTimer/cancelTransientTimer/teardownChildrenRec) has ZERO
// core dependencies (os/exec + os.Executable + a self-invoked `charly fleet del`) and needed no
// seam even before this move — confirmed by the unit-1 design note this cutover executes.

// loadFleetConfig reads the per-host deploy overlay via the cycle-free plugin-side helper
// loaderkit.LoadHostFleetConfigViaExecutor (#55 coneC Unit C2 — this retired the former
// deploykit.LoadFleetConfigViaSeam host-handler round-trip; loaderkit already imports deploykit,
// so the helper lives there and a plugin calls it directly — placement-invariant, works identically
// compiled-in or out-of-process). R3 hoist
// (charly#176 round 1): the former LoadFleetConfigViaSeam itself hoisted four near-identical
// local copies (candy/plugin-pod/remove_orchestration.go's resolveSidecarNames,
// candy/plugin-status/nested_tree.go, candy/plugin-substrate/status_flat.go, this one); the C2
// helper is now the ONE shared implementation all four call. Returns (nil, nil) on an
// absent/empty overlay, matching deploykit.LoadFleetConfig's own contract.
func loadFleetConfig() (*deploykit.FleetConfig, error) {
	return loaderkit.LoadHostFleetConfigViaExecutor(cmdCtx, cmdExec)
}

// ephemeralHandle captures the runtime state returned by registerEphemeral and consumed by
// teardownEphemeral. Internal to this plugin — the host discards the register reply's payload
// entirely (the vm caller only ever checks the error), so this never crosses the
// wire and needs no CUE def.
type ephemeralHandle struct {
	id              string
	deployName      string
	instanceName    string
	timerUnit       string
	ttlDeadline     time.Time
	parentVm        string
	parentSnapshot  string
	parentEphemeral string
}

// registerEphemeral serves OpEphemeralRegister: generate the instance id, resolve nesting +
// TTL, register the systemd TTL safety net, bump the vm-snapshot + parent-child refcounts, and
// persist the EphemeralRuntime into charly.yml. Best-effort throughout (warnings to stderr, never
// fatal) — matching the prior in-core dispatch contract exactly.
func registerEphemeral(node *spec.Deploy, deployName string) (*ephemeralHandle, error) {
	if node == nil || !node.IsEphemeral() {
		return nil, fmt.Errorf("registerEphemeral: node %q is not marked ephemeral", deployName)
	}

	id, err := deploykit.NewEphemeralID()
	if err != nil {
		return nil, fmt.Errorf("generating ephemeral id: %w", err)
	}

	parentEph := os.Getenv("CHARLY_EPHEMERAL_PARENT")
	ttl, err := effectiveEphemeralTTL(node, parentEph)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(ttl)

	pattern := node.Ephemeral.EffectiveNamingPattern()
	instanceName, err := deploykit.RenderNamingPattern(pattern, deployName, id)
	if err != nil {
		return nil, fmt.Errorf("rendering naming_pattern %q: %w", pattern, err)
	}

	// Register the transient timer FIRST — panic-safe ordering: the TTL safety net is in place
	// even if a later step fails.
	timerUnit, err := registerTransientTimer(deployName, ttl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: registering TTL transient timer: %v (continuing without TTL safety net)\n", err)
		timerUnit = ""
	}

	handle := &ephemeralHandle{
		id:              id,
		deployName:      deployName,
		instanceName:    instanceName,
		timerUnit:       timerUnit,
		ttlDeadline:     deadline,
		parentEphemeral: parentEph,
	}

	// vm-target (ssh venue) snapshot refcount.
	if descentVenue(node) == "ssh" && node.From != "" && node.FromSnapshot != "" {
		if err := vmshared.IncrementSnapshotRefcount(node.From, node.FromSnapshot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: incrementing snapshot refcount: %v\n", err)
		}
		handle.parentVm = node.From
		handle.parentSnapshot = node.FromSnapshot
	}

	if err := persistEphemeralRuntime(node, deployName, handle); err != nil {
		fmt.Fprintf(os.Stderr, "warning: persisting ephemeral runtime: %v\n", err)
	}

	if parentEph != "" {
		_ = bumpParentChildRefcount(parentEph, +1)
	}

	return handle, nil
}

// teardownEphemeral serves OpEphemeralTeardown: recursively del nested children, cancel the
// transient timer, decrement refcounts, and clear the charly.yml lifecycle metadata. Matches the
// prior in-core TeardownEphemeralLifecycle contract exactly.
func teardownEphemeral(node *spec.Deploy, deployName string) error {
	// RCA #9 finding #11 (FINAL/K5 unit 6a): the SAME bug class as vmLifecyclePostTeardown's
	// finding #10, one call deeper. node.IsEphemeral() checks Deploy.Ephemeral != nil — the
	// AUTHORED ephemeral: {ttl: ...} declaration — never carried by an overlay-loaded node
	// (ephemeralFallbackNode seeds only Target/From). The caller here is ALWAYS
	// vmLifecyclePostTeardown's overlay-loaded dcNode (TeardownEphemeralLifecycle's one
	// caller), so this guard rejected EVERY real teardown before it could reach the logic
	// below, which ALREADY correctly reads node.VmState.Ephemeral throughout — matching that
	// established pattern here too.
	if node == nil || node.VmState == nil || node.VmState.Ephemeral == nil {
		return fmt.Errorf("teardownEphemeral: node %q is not marked ephemeral", deployName)
	}

	if err := teardownChildren(deployName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: nested ephemeral teardown: %v\n", err)
	}

	if node.VmState != nil && node.VmState.Ephemeral != nil && node.VmState.Ephemeral.TimerUnit != "" {
		cancelTransientTimer(node.VmState.Ephemeral.TimerUnit)
	}

	if descentVenue(node) == "ssh" && node.From != "" && node.FromSnapshot != "" {
		if err := vmshared.DecrementSnapshotRefcount(node.From, node.FromSnapshot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: decrementing snapshot refcount: %v\n", err)
		}
	}

	if node.VmState != nil && node.VmState.Ephemeral != nil && node.VmState.Ephemeral.ParentEphemeral != "" {
		_ = bumpParentChildRefcount(node.VmState.Ephemeral.ParentEphemeral, -1)
	}

	if err := clearEphemeralRuntime(deployName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: clearing ephemeral runtime: %v\n", err)
	}
	return nil
}

// descentVenue reads the node's stamped Descent.Venue directly — the FAST path of charly core's
// former nodeTraits (deploy_tree.go): by the time a node reaches Add/Del, LoadUnified's
// stampFleetDescents has already stamped every fleet node's Descent, so the registry-backed
// fallback (deployTraitsFor, a core-only Mechanism this plugin cannot reach) is never needed
// here — mirroring group/pod-lifecycle's OWN plugin-local descent reads (the C2-substrate
// precedent: a plugin reads the already-stamped field, never re-derives it from the registry).
func descentVenue(node *spec.Deploy) string {
	if node == nil || node.Descent == nil {
		return ""
	}
	return node.Descent.Venue
}

// effectiveEphemeralTTL computes the TTL for a deploy, clipping to the parent ephemeral's
// remaining TTL when nested. parentID may be empty. The reverse-channel-coupled parent LOOKUP
// (lookupEphemeralByID → loadFleetConfig, the loaderkit overlay read) is not unit-testable
// standalone (needs a live reverse channel — covered by the bed instead); the CLIPPING MATH
// itself is pulled into clipTTLToParent, which IS unit-tested (ephemeral_test.go), mirroring
// candy/plugin-pod/remove_orchestration.go's sidecarNamesFromFleetConfig split.
func effectiveEphemeralTTL(node *spec.Deploy, parentID string) (time.Duration, error) {
	declared := node.Ephemeral.EffectiveTTL()
	if parentID == "" {
		return declared, nil
	}
	parent, err := lookupEphemeralByID(parentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: parent ephemeral %q not found; using declared TTL %s\n", parentID, declared)
		return declared, nil
	}
	return clipTTLToParent(declared, parentID, parent)
}

// clipTTLToParent is the pure clipping math effectiveEphemeralTTL applies once it has a resolved
// parent EphemeralRuntime: an empty or unparseable TtlDeadline is a no-op (declared TTL stands);
// an already-expired parent is a hard error; a declared TTL exceeding the parent's remaining time
// is clipped down (logged), otherwise the declared TTL stands.
func clipTTLToParent(declared time.Duration, parentID string, parent *spec.EphemeralRuntime) (time.Duration, error) {
	if parent.TtlDeadline == "" {
		return declared, nil
	}
	deadline, err := time.Parse(time.RFC3339, parent.TtlDeadline)
	if err != nil {
		return declared, nil
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, fmt.Errorf("parent ephemeral %q has already expired (deadline %s)", parentID, parent.TtlDeadline)
	}
	if declared > remaining {
		fmt.Fprintf(os.Stderr, "note: clipping ephemeral TTL from %s to parent's remaining %s\n", declared, remaining)
		return remaining, nil
	}
	return declared, nil
}

// ephemeralTimerUnitPrefix is the STABLE (timestamp-free) prefix of the transient timer unit
// registerTransientTimer creates for deployName — the SAME formula, pulled out as its own pure
// function purely for testability (registerTransientTimer itself shells out to systemd-run, not
// unit-testable standalone). RCA #4 (FINAL/K5 unit 6a, live-bed-caught): the FULL dotted deploy
// address is sanitized here — deployName is "check-sidecar-pod.check-sidecar-pod-ephvm" for a
// nested member, NOT the leaf name alone — a caller (a check assertion, an operator script)
// greping for "charly-fleet-del-<leaf-name>" will never match; grep for THIS prefix instead
// (registerTransientTimer appends "-<unix-ts>.timer" after it, so grep, never an exact match).
func ephemeralTimerUnitPrefix(deployName string) string {
	return "charly-fleet-del-" + deploykit.SanitizeUnitName(deployName)
}

// registerTransientTimer creates a systemd-run --user --on-active=<ttl> transient unit that fires
// `charly fleet del <deployName> --assume-yes` when the TTL elapses. Falls back to a no-op when
// systemd-run is not available.
//
// RCA (bed-robustness batch, item 1 — weeks of failures, ~21 recorded): the transient unit fired
// `charly fleet del <deployName>` with NO working directory pinned. A `--user` systemd-run unit's
// ExecStart runs under the user systemd manager's OWN default WorkingDirectory — the user's home
// (`/home/<user>`), NOT the cwd `charly fleet add` was invoked from — so the self-exec'd charly
// resolved its project dir against `$HOME`, found no `charly.yml` there, and failed EVERY fire with
// "no charly.yml found in /home/<user>". `os.Getwd()` at REGISTRATION time is the project directory
// (main.go's `os.Chdir(cli.Dir)` has already run by the time any command body executes), so pinning
// it via `--working-directory=<wd>` on the transient unit is the fix — the SAME primitive systemd-run
// exposes for exactly this class of problem (no sleep/retry, no environment-variable workaround).
func registerTransientTimer(deployName string, ttl time.Duration) (string, error) {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return "", fmt.Errorf("systemd-run not in PATH; TTL safety net disabled")
	}
	// DELIBERATELY os.Executable(), not a resolved installed `charly`. Pointing the unit at a
	// stable installed path is the obvious answer to "the worktree binary gets deleted", and it is
	// wrong twice over. First, `fleet del` is correctly NOT freshness-safe, so an installed binary
	// older than the source tree REFUSES to run — the normal state on a developer host, i.e. the
	// reaper would fail exactly where this defect lives. Second and worse: the identity gate that
	// makes the freshness bypass safe lives in the REAPING binary, so an installed binary predating
	// that gate would reap with no incarnation check at all. The safety property depends on the
	// vintage of the binary that runs, not the one that registers.
	//
	// The worktree-deletion case is handled where it belongs instead — the bed cancels its own
	// timers at teardown (candy/plugin-check/bed_ephemeral_timers.go), so a unit pointing into a
	// removed worktree should not exist; if one does, it fails visibly at 203/EXEC rather than
	// silently reaping on a stale identity.
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating charly binary: %w", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolving working directory: %w", err)
	}
	unitName := fmt.Sprintf("%s-%d", ephemeralTimerUnitPrefix(deployName), time.Now().Unix())
	deployConfig := os.Getenv(spec.DeployConfigEnv)
	args := registerTransientTimerArgs(unitName, ttl, wd, exe, deployConfig, reaperDelArgv(deployName, unitName))
	cmd := exec.Command("systemd-run", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("systemd-run: %w", err)
	}
	return unitName + ".timer", nil
}

// registerTransientTimerArgs builds the systemd-run argv registerTransientTimer shells out with —
// pulled out as its own pure function purely for testability (registerTransientTimer itself shells
// out to systemd-run, not unit-testable standalone). `--working-directory=<wd>` is the load-bearing
// fix: without it the transient unit's ExecStart runs from the user systemd manager's default
// WorkingDirectory (the user's home), never the project directory the deploy was registered from.
func registerTransientTimerArgs(unitName string, ttl time.Duration, wd, exe, deployConfig string, delArgv []string) []string {
	args := []string{
		"--user",
		"--unit=" + unitName,
		"--on-active=" + ttl.String(),
		"--working-directory=" + wd,
		// INCARNATION IDENTITY. The reaper argv is `fleet del <entity> --assume-yes` — it names the
		// ENTITY, never the incarnation, and bed entity names are REUSED run over run. Registration
		// also overwrites the single recorded EphemeralRuntime.TimerUnit, so every earlier run's
		// timer stays armed and permanently un-cancellable (cancelTransientTimer only ever reaches
		// the most recently recorded unit). A stale timer's `fleet del` is therefore byte-identical
		// to a legitimate one and would delete whichever incarnation currently holds the name.
		//
		// Passing the unit its OWN name lets the teardown compare it against the recorded
		// TimerUnit and no-op when it has been superseded. Observed 2026-08-15: nine stale timers
		// fired at 00:19:26 while a live incarnation held the entity name; only a missing
		// executable (203/EXEC) stopped them. Fixing the executable path WITHOUT this guard would
		// have converted a silent leak into deletion of live work — and it would have looked like
		// a successful fix, because the reaper would finally be running.
		// The reaper is a machine-invoked TTL janitor, not an interactive command. The freshness
		// guard refuses non-read-only verbs (`fleet del` is correctly NOT in isFreshnessSafeVerb)
		// whenever the source tree is newer than the running binary — the normal state on a
		// developer host, and measured on this one. Without this the reaper simply fails a
		// different way and the VM leaks exactly as before. Scoped to THIS unit's environment: no
		// human invocation is affected.
		"--setenv=CHARLY_SKIP_FRESHNESS_CHECK=1",
	}
	// STATE LIFETIME. A bed session isolates its ephemeral deploy state into a per-run temp overlay
	// (bed_session.go: MkdirTemp + Setenv(CHARLY_DEPLOY_CONFIG)) and REMOVES it at teardown, while
	// unsetting the variable. A timer firing later therefore reads the OPERATOR overlay, which has
	// no record of the entity — so it can neither resolve it nor verify its identity. Carrying the
	// path the registration was made under makes the timer's state lifetime match the timer's own:
	// on a normal teardown the bed cancels the timer AND removes the dir, so nothing is left
	// stale; on an ABNORMAL exit neither runs, the dir survives, and the reaper can still work —
	// which is precisely the case a TTL net exists for. The isolation is preserved exactly: each
	// unit reads its OWN bed's overlay, never the operator's and never a peer's.
	if deployConfig != "" {
		args = append(args, "--setenv="+spec.DeployConfigEnv+"="+deployConfig)
	}
	args = append(args, exe)
	return append(args, delArgv...)
}

// reaperDelArgv builds the `fleet del` argv the TTL unit executes.
//
// The "vm:" form is used for EVERY registration. `resolveDelNode` resolves a plain dotted address
// only through the project tree, and a reaper runs with no project — the worktree it was registered
// from is routinely deleted once its PR lands — so only the prefixed form reaches the tree-absent
// fallback. The prefix is what makes the reaper work without a project.
//
// It used to be unsafe to give that form to registrations whose state does not outlive their
// session, because the fallback synthesises a node from the ADDRESS ALONE and a stale timer would
// then delete whichever incarnation holds the reused name. That is no longer a property of the
// address: --require-timer-unit makes FleetDelCmd verify the incarnation BEFORE it resolves
// anything, and refuse when no registration is recorded. Identity is checked first, so the
// fallback is safe for every population and the address form is uniform.
func reaperDelArgv(deployName, unitName string) []string {
	return append(spec.FleetDelArgv("vm:"+deployName), "--require-timer-unit="+unitName)
}

// cancelTransientTimerFn is the cancel call as a package var, for testability — the seam shape
// plugin-clean uses for liveBuildFloor. Production always points at cancelTransientTimer.
var cancelTransientTimerFn = cancelTransientTimer

// cancelTransientTimer stops a previously registered transient unit. Best-effort.
func cancelTransientTimer(unit string) {
	if unit == "" {
		return
	}
	cmd := exec.Command("systemctl", "--user", "stop", unit)
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

// persistEphemeralRuntime writes the ephemeralHandle into charly.yml's vm_state.ephemeral (or
// pod_state / kubernetes_state for those targets).
// ephemeralOverlayKey computes the dc.Fleet map key for an ephemeral entry — the SAME
// dot-sanitized "vm:<domain-identity>" scheme deploykit.SaveVmDeployState (sdk/deploykit/
// vm_deploy_state.go) already uses (via candy/plugin-vm/vm_create_orchestrate.go's hostConfigPersist +
// sdk/vmshared.VmDomainIdentity's explicit "." → "-" replacement), NEVER the raw (possibly
// dotted) deployName directly. RCA #2 (FINAL/K5 unit 6a, the check-sidecar-pod bed's SECOND
// failure): the raw dotted key round-tripped through kind discrimination fine after the
// Target/From fix, but was then rejected by the loader's SEPARATE "a deployment key must not
// contain '.'" check on the very next read (ValidateDeploymentName, sdk/spec/deploy_tree_validate.go) — dots
// are reserved for dotted-PATH ADDRESSING (`charly fleet del a.b.c`), never a literal dc.Fleet
// map key. Using the SAME key as SaveVmDeployState has a bonus: ephemeral state and vm state
// (ssh_port, disk_path) end up in ONE overlay entry instead of two — persistEphemeralRuntime's
// `!ok` fallback covers the edge where ephemeral registration runs BEFORE the vm's own state
// gets persisted at all (the entry does not exist yet), not "every ephemeral registration ever".
// RCA #7 (FINAL/K5 unit 6a, live-probe-caught, updated from an earlier "ordering artifact, not
// the common case" note that RCA #6's key unification proved WRONG): the vm ephemeral register
// (candy/plugin-deploy-vm's dispatchVmEphemeralRegister) runs BEFORE `charly vm create`'s own state writes (the port_auto persist) EVERY TIME —
// the vm ephemeral register's call order, not incidental — so the two writers landing on this
// SAME canonical key (post-RCA-#6) is the COMMON case, and the interaction is LOAD-BEARING: a
// naive wholesale `entry.VmState = state` in SaveVmDeployState would silently ERASE the
// just-registered Ephemeral block on every ordinary Add. SaveVmDeployState's own Ephemeral-
// preservation merge (sdk/deploykit/vm_deploy_state.go) is what makes that safe — see its doc comment.
// Scoped to vm only (VmDomainIdentity is vm/libvirt-domain-specific naming) — correct today
// since ephemeral is vm-only (validate_ephemeral.go); pod/kubernetes pick their OWN key scheme when
// the bed-robustness batch wires their Add/Del paths to this seam.
func ephemeralOverlayKey(deployName string) string {
	return "vm:" + vmshared.VmDomainIdentity(deployName)
}

// ephemeralFallbackNode builds the FleetNode used when dc.Fleet[deployName] has no existing
// per-host-overlay entry (the common first-registration case). FINAL/K5 unit 6a fix (real bug,
// live-bed-caught): a bare spec.FleetNode{} here left Target/From EMPTY — on the next reload,
// deploy_nodeform.go's fleetDiscForEntity sees no target + no pod-workload indicator and
// discriminates the persisted entry as "group", whose closed #GroupInput schema then rejects the
// leftover vm_state field (a hard load failure on every subsequent per-host-overlay read). Seed
// ONLY the identifying fields (Target/From) from the authored node — an overlay entry is STATE,
// never structure, so Children/Members are deliberately NOT copied. This mirrors the
// ALREADY-WORKING sdk/deploykit/vm_deploy_state.go:SaveVmDeployState, which sets Target="vm"
// unconditionally on a fresh entry — independent proof dotted deploy identities round-trip
// correctly through dc.Fleet once Target/From are set (the identity itself was never the
// problem). Pulled out as its own function for unit testability — persistEphemeralRuntime itself
// needs a live reverse channel (loadFleetConfig/saveDeployConfig) a standalone test can't drive.
func ephemeralFallbackNode(authored *spec.Deploy) spec.FleetNode {
	node := spec.FleetNode{}
	if authored != nil {
		node.Target = authored.Target
		node.From = authored.From
	}
	return node
}

// ensureEphemeralFleetConfig returns dc with a GUARANTEED non-nil *FleetConfig AND non-nil
// Fleet map. RCA #5 (FINAL/K5 unit 6a, live-probe-caught): a nil *FleetConfig (no overlay file
// at all) was already guarded at persistEphemeralRuntime's call site, but loadFleetConfig can
// ALSO return a non-nil *FleetConfig whose Fleet field is itself nil — the exact shape of a
// genuinely FRESH per-host overlay (a bed's brand-new tmp file, or any operator overlay with no
// `fleet:` section yet decoded from valid-but-fleet-less JSON). Reading dc.Fleet[key] from a
// nil map is safe (ok=false), but persistEphemeralRuntime's !ok branch FABRICATES a fresh entry
// and falls through to a WRITE — unlike clearEphemeralRuntime/bumpParentChildRefcount, which both
// return/continue before ever writing on a nil-map miss (verified safe-by-construction, no fix
// needed there) — so persistEphemeralRuntime is the one writer that needed this: without it,
// `panic: assignment to entry in nil map` on EVERY fresh-overlay registration, previously masked
// because the panic was swallowed by the in-proc plugin dispatch (now made loud —
// recoverEphemeralOpPanic, command.go). Pulled out as its own function purely for testability
// (persistEphemeralRuntime itself needs the seam-coupled loadFleetConfig, not unit-testable
// standalone).
func ensureEphemeralFleetConfig(dc *deploykit.FleetConfig) *deploykit.FleetConfig {
	if dc == nil {
		dc = &deploykit.FleetConfig{}
	}
	if dc.Fleet == nil {
		dc.Fleet = map[string]spec.FleetNode{}
	}
	return dc
}

func persistEphemeralRuntime(authored *spec.Deploy, deployName string, h *ephemeralHandle) error {
	return mutateDeployConfig(func(dc *deploykit.FleetConfig) (bool, error) {
		persistEphemeralInto(dc, authored, deployName, h)
		return true, nil
	})
}

// persistEphemeralInto writes the ephemeral lifecycle block onto a FRESH overlay read under the
// deploy-config lock — the shape mutateDeployConfig requires. Registering against fresh state is
// load-bearing here: the ephemeral registrar runs concurrently with `charly vm create`'s own state
// writes for the SAME key (the RCA #7 ordering contract SaveVmDeployState documents), so a
// snapshot write-back would drop whichever of the two loaded first.
func persistEphemeralInto(dc *deploykit.FleetConfig, authored *spec.Deploy, deployName string, h *ephemeralHandle) {
	dc = ensureEphemeralFleetConfig(dc)
	key := ephemeralOverlayKey(deployName)
	node, ok := dc.Fleet[key]
	if !ok {
		node = ephemeralFallbackNode(authored)
	}
	if node.VmState == nil {
		node.VmState = &spec.VmDeployState{}
	}
	// CANCEL THE TIMER THIS RECORD IS ABOUT TO ORPHAN. EphemeralRuntime holds ONE TimerUnit, so
	// overwriting it strands the previous registration's timer: still armed, no longer referenced
	// by anything, and therefore un-cancellable by any later teardown — which reads only the
	// recorded unit. That is how ten armed units accumulated for one reused entity name, and how a
	// single bed run left three (it registers once per deploy/rebuild phase, and only the last was
	// cancellable at teardown).
	//
	// Cancelling here makes at most ONE timer armed per entity at any moment, which fixes three
	// things at their source rather than downstream: the within-run surplus, the cross-run pile,
	// and — most importantly — the stale-timer hazard itself. A run that deploys over a name whose
	// previous incarnation was killed cancels that armed timer HERE, so it never survives into the
	// run it could damage. The identity gate stops a stale timer from ACTING; this stops it from
	// EXISTING.
	//
	// Both are kept deliberately, and each covers a case the other does not: cancelTransientTimer
	// is best-effort, so a systemctl failure leaves a timer armed with no second chance — and that
	// is precisely what the gate refuses at fire time. Do not delete either as redundant.
	if prior := node.VmState.Ephemeral; prior != nil && prior.TimerUnit != "" && prior.TimerUnit != h.timerUnit {
		cancelTransientTimerFn(prior.TimerUnit)
	}
	node.VmState.Ephemeral = &spec.EphemeralRuntime{
		ID:              h.id,
		ParentVm:        h.parentVm,
		ParentSnapshot:  h.parentSnapshot,
		ParentEphemeral: h.parentEphemeral,
		TimerUnit:       h.timerUnit,
		TtlDeadline:     h.ttlDeadline.Format(time.RFC3339),
		Status:          "active",
		InstanceName:    h.instanceName,
		// The REAL CLI-addressable identity (dotted tree path for a nested deploy) — distinct
		// from `key`, the dot-sanitized dc.Fleet map key above. teardownChildrenRec reads this
		// back for its recursive `charly fleet del` call, since the map key itself is not
		// reversible to the original address.
		DeployAddress: deployName,
	}
	dc.Fleet[key] = node
}

// clearEphemeralRuntime removes the lifecycle metadata at teardown. Checked against the
// FINAL/K5 unit 6a bed-caught bug (persistEphemeralRuntime's blank-fallback-node →
// kind-misdiscrimination chain): this function does NOT need the same fix — it already
// `return nil`s on `!ok` rather than fabricating a blank node, so it can never write an
// under-specified entry.
func clearEphemeralRuntime(deployName string) error {
	return mutateDeployConfig(func(dc *deploykit.FleetConfig) (bool, error) {
		key := ephemeralOverlayKey(deployName)
		node, ok := dc.Fleet[key]
		if !ok {
			return false, nil
		}
		if node.VmState == nil || node.VmState.Ephemeral == nil {
			return false, nil
		}
		node.VmState.Ephemeral = nil
		dc.Fleet[key] = node
		return true, nil
	})
}

// bumpParentChildRefcount adjusts the parent ephemeral's child counter by delta (+1 on nested
// register, -1 on nested teardown). Checked against the same bug class as clearEphemeralRuntime:
// it only mutates an entry found by its `range` loop, which already requires
// node.VmState.Ephemeral != nil — it never fabricates a new entry, so it cannot write an
// under-specified one either.
func bumpParentChildRefcount(parentID string, delta int) error {
	// The read-modify-write must be ONE locked cycle: a refcount is the classic lost-update
	// victim — two concurrent nested register/teardown writers each reading the same prior count
	// and writing back their own increment leaves the parent with one child's worth of refcount
	// and a premature teardown.
	return mutateDeployConfig(func(dc *deploykit.FleetConfig) (bool, error) {
		for name, node := range dc.Fleet {
			if node.VmState == nil || node.VmState.Ephemeral == nil {
				continue
			}
			if node.VmState.Ephemeral.ID != parentID {
				continue
			}
			node.VmState.Ephemeral.ChildRefcount += delta
			if node.VmState.Ephemeral.ChildRefcount < 0 {
				node.VmState.Ephemeral.ChildRefcount = 0
			}
			dc.Fleet[name] = node
			return true, nil
		}
		return false, nil
	})
}

// lookupEphemeralByID scans charly.yml for the ephemeral with the given ID. Used for nested TTL
// clipping. The seam-coupled LOAD (loadFleetConfig) is not unit-testable standalone; the pure
// scan is split into ephemeralByIDFromFleetConfig (ephemeral_test.go tests that directly).
func lookupEphemeralByID(id string) (*spec.EphemeralRuntime, error) {
	dc, err := loadFleetConfig()
	if err != nil || dc == nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	return ephemeralByIDFromFleetConfig(dc, id)
}

// ephemeralByIDFromFleetConfig is the pure scan lookupEphemeralByID applies once it has an
// already-loaded FleetConfig.
func ephemeralByIDFromFleetConfig(dc *deploykit.FleetConfig, id string) (*spec.EphemeralRuntime, error) {
	for _, node := range dc.Fleet {
		if node.VmState == nil || node.VmState.Ephemeral == nil {
			continue
		}
		if node.VmState.Ephemeral.ID == id {
			return node.VmState.Ephemeral, nil
		}
	}
	return nil, fmt.Errorf("ephemeral with id %q not found", id)
}

// teardownChildren recursively dels nested ephemerals whose parent is the deploy with the given
// name's ephemeral ID. Depth-first; visited-set guards against cycles.
func teardownChildren(deployName string) error {
	dc, err := loadFleetConfig()
	if err != nil || dc == nil {
		return err
	}
	key := ephemeralOverlayKey(deployName)
	parentID := ""
	if node, ok := dc.Fleet[key]; ok && node.VmState != nil && node.VmState.Ephemeral != nil {
		parentID = node.VmState.Ephemeral.ID
	}
	if parentID == "" {
		return nil
	}
	// Seed visited with OUR OWN dc.Fleet key (the sanitized form) — teardownChildrenRec's cycle
	// guard compares against the map's native (already-sanitized) keys, never the raw deployName.
	visited := map[string]bool{key: true}
	return teardownChildrenRec(dc, parentID, visited)
}

func teardownChildrenRec(dc *deploykit.FleetConfig, parentID string, visited map[string]bool) error {
	var toDel []string
	for name, node := range dc.Fleet {
		if visited[name] {
			continue
		}
		if node.VmState == nil || node.VmState.Ephemeral == nil {
			continue
		}
		if node.VmState.Ephemeral.ParentEphemeral != parentID {
			continue
		}
		toDel = append(toDel, name)
	}
	for _, name := range toDel {
		visited[name] = true
		// The REAL CLI-addressable identity (persistEphemeralRuntime's DeployAddress), NOT the
		// dc.Fleet map key `name` itself — the key is a dot-sanitized "vm:<domain-id>" form
		// that `charly fleet del` cannot resolve back to the original (possibly dotted) deploy
		// tree address. Falls back to `name` only for a pre-fix entry that predates this field
		// (best-effort — such an entry is already a latent leak from before this cutover).
		delTarget := name
		if node, ok := dc.Fleet[name]; ok && node.VmState != nil && node.VmState.Ephemeral != nil {
			if node.VmState.Ephemeral.DeployAddress != "" {
				delTarget = node.VmState.Ephemeral.DeployAddress
			}
			if err := teardownChildrenRec(dc, node.VmState.Ephemeral.ID, visited); err != nil {
				return err
			}
		}
		// Invoke `charly fleet del <child> --assume-yes` — shelling out so the child's full
		// cleanup (including its own teardownEphemeral) runs.
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := exec.Command(exe, spec.FleetDelArgv(delTarget)...)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: nested teardown of %q failed: %v\n", delTarget, err)
		}
	}
	return nil
}
