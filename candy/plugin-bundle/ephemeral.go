package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// ephemeral.go — the FINAL/K5 unit-6a move of charly/ephemeral_lifecycle.go: cross-substrate
// ephemeral-deploy lifecycle (systemd TTL transient timer, parent/child nesting, vm-snapshot
// refcounts, charly.yml persistence). command:bundle is the substrate-neutral deploy-lifecycle
// owner: this body is written substrate-agnostic (no vm/pod/k8s branch of its own), reached via
// the SAME OpEphemeralRegister/OpEphemeralTeardown legs regardless of which substrate calls
// them. **Only the VM substrate actually calls them TODAY** (vm_lifecycle_preresolve.go, via
// deploy_add_shared.go's registerEphemeralIfMarked) — pod and k8s Add/Del never reach this code
// (verified by call-graph, not the deleted charly/ephemeral_lifecycle.go's own header, which
// falsely claimed "all three target types... call into these functions" — an R1 false-comment
// instance this move does NOT repeat). Wiring pod/k8s's Add/Del paths to call it too is tracked
// as its own bed-robustness-batch item (their dispatch lives in candy/plugin-deploy-pod /
// candy/plugin-pod, outside this unit's scope); `ephemeral: true` on a pod/k8s deploy is
// rejected at load time in the meantime (charly/validate_ephemeral.go) rather than silently
// no-op'd. Config persistence goes through the ALREADY-EXISTING seam pair on BOTH legs: reads route
// through "pod-config-load-bundle" (loadBundleConfig below) and writes through "deploy-config-save"
// (saveDeployConfig, config_cmd.go). Calling deploykit.LoadBundleConfig() directly — relying on the
// compiled-in placement's shared process-wide deploykit.DeployStateHost var — is the
// placement-dependent silent-degradation anti-pattern this program has already fixed twice
// (candy/plugin-pod's resolveSidecarNames + engine-resolution, remove_orchestration.go): correct
// only because command:bundle happens to be compiled-in TODAY, a per-BUILD fact never an authoring
// guarantee (the dual-placement Key Rule) — silently empty out-of-process instead of the loud
// HostBuild-transport error the seam gives. This also fixes the deploy_file.go:99 silent-nil
// footgun for BOTH placements uniformly, not just the compiled-in one.
//
// The vm-snapshot refcount calls (vmshared.Increment/DecrementSnapshotRefcount) are
// ALREADY sdk-portable (sdk/vmshared) — reached directly, no alias, no seam. The systemd
// self-exec half (registerTransientTimer/cancelTransientTimer/teardownChildrenRec) has ZERO
// core dependencies (os/exec + os.Executable + a self-invoked `charly bundle del`) and needed no
// seam even before this move — confirmed by the unit-1 design note this cutover executes.

// loadBundleConfig reads the per-host deploy overlay via the "pod-config-load-bundle" HostBuild
// seam (placement-invariant — works identically compiled-in or out-of-process), mirroring
// candy/plugin-pod/remove_orchestration.go's resolveSidecarNames. Returns (nil, nil) on an
// absent/empty overlay, matching deploykit.LoadBundleConfig's own contract.
func loadBundleConfig() (*deploykit.BundleConfig, error) {
	var rep spec.PodConfigLoadBundleReply
	if err := hostDeploySeamJSON(podConfigLoadBundleSeamKind, spec.PodConfigLoadDeployRequest{Caller: "command:bundle ephemeral lifecycle"}, &rep); err != nil {
		return nil, err
	}
	if len(rep.ConfigJSON) == 0 {
		return nil, nil
	}
	var dc deploykit.BundleConfig
	if err := json.Unmarshal(rep.ConfigJSON, &dc); err != nil {
		return nil, err
	}
	return &dc, nil
}

// podConfigLoadBundleSeamKind names the EXISTING "pod-config-load-bundle" HostBuild kind (no
// shared Go const across modules — each consumer names its own, the established convention, e.g.
// candy/plugin-pod/remove_orchestration.go's podConfigLoadBundleKind).
const podConfigLoadBundleSeamKind = "pod-config-load-bundle"

// ephemeralHandle captures the runtime state returned by registerEphemeral and consumed by
// teardownEphemeral. Internal to this plugin — the host discards the register reply's payload
// entirely (registerEphemeralIfMarked only ever checked the error), so this never crosses the
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
// fatal) — matching the prior in-core RegisterEphemeralLifecycle contract exactly.
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
// stampBundleDescents has already stamped every bundle node's Descent, so the registry-backed
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
// remaining TTL when nested. parentID may be empty. The seam-coupled parent LOOKUP
// (lookupEphemeralByID → loadBundleConfig, "pod-config-load-bundle") is not unit-testable
// standalone (needs a live reverse channel — covered by the bed instead); the CLIPPING MATH
// itself is pulled into clipTTLToParent, which IS unit-tested (ephemeral_test.go), mirroring
// candy/plugin-pod/remove_orchestration.go's sidecarNamesFromBundleConfig split.
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
// greping for "charly-bundle-del-<leaf-name>" will never match; grep for THIS prefix instead
// (registerTransientTimer appends "-<unix-ts>.timer" after it, so grep, never an exact match).
func ephemeralTimerUnitPrefix(deployName string) string {
	return "charly-bundle-del-" + deploykit.SanitizeUnitName(deployName)
}

// registerTransientTimer creates a systemd-run --user --on-active=<ttl> transient unit that fires
// `charly bundle del <deployName> --assume-yes` when the TTL elapses. Falls back to a no-op when
// systemd-run is not available.
func registerTransientTimer(deployName string, ttl time.Duration) (string, error) {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return "", fmt.Errorf("systemd-run not in PATH; TTL safety net disabled")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating charly binary: %w", err)
	}
	unitName := fmt.Sprintf("%s-%d", ephemeralTimerUnitPrefix(deployName), time.Now().Unix())
	args := append([]string{
		"--user",
		"--unit=" + unitName,
		"--on-active=" + ttl.String(),
		exe,
	}, ephemeralDeployDelArgv(deployName)...)
	cmd := exec.Command("systemd-run", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("systemd-run: %w", err)
	}
	return unitName + ".timer", nil
}

// cancelTransientTimer stops a previously registered transient unit. Best-effort.
func cancelTransientTimer(unit string) {
	if unit == "" {
		return
	}
	cmd := exec.Command("systemctl", "--user", "stop", unit)
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

// ephemeralDeployDelArgv mirrors charly core's bundle_add_cmd.go:deployDelArgv (a trivial pure
// helper, duplicated rather than shared across the module boundary — bundle_add_cmd.go itself
// stays core, a candidate-floor sibling of the FLOOR-SLIM adjudication).
func ephemeralDeployDelArgv(name string) []string {
	return []string{"bundle", "del", name, "--assume-yes"}
}

// persistEphemeralRuntime writes the ephemeralHandle into charly.yml's vm_state.ephemeral (or
// pod_state / k8s_state for those targets).
// ephemeralOverlayKey computes the dc.Bundle map key for an ephemeral entry — the SAME
// dot-sanitized "vm:<domain-identity>" scheme charly/vm_deploy_state.go's saveVmDeployState
// already uses (via candy/plugin-vm/vm_create_orchestrate.go's hostConfigPersist +
// sdk/vmshared.VmDomainIdentity's explicit "." → "-" replacement), NEVER the raw (possibly
// dotted) deployName directly. RCA #2 (FINAL/K5 unit 6a, the check-sidecar-pod bed's SECOND
// failure): the raw dotted key round-tripped through kind discrimination fine after the
// Target/From fix, but was then rejected by the loader's SEPARATE "a deployment key must not
// contain '.'" check on the very next read (ValidateDeploymentName, sdk/spec/deploy_tree_validate.go) — dots
// are reserved for dotted-PATH ADDRESSING (`charly bundle del a.b.c`), never a literal dc.Bundle
// map key. Using the SAME key as saveVmDeployState has a bonus: ephemeral state and vm state
// (ssh_port, disk_path) end up in ONE overlay entry instead of two — persistEphemeralRuntime's
// `!ok` fallback covers the edge where ephemeral registration runs BEFORE the vm's own state
// gets persisted at all (the entry does not exist yet), not "every ephemeral registration ever".
// RCA #7 (FINAL/K5 unit 6a, live-probe-caught, updated from an earlier "ordering artifact, not
// the common case" note that RCA #6's key unification proved WRONG): registerEphemeralIfMarked
// runs BEFORE `charly vm create`'s own state writes (the port_auto persist) EVERY TIME —
// vm_lifecycle_preresolve.go's call order, not incidental — so the two writers landing on this
// SAME canonical key (post-RCA-#6) is the COMMON case, and the interaction is LOAD-BEARING: a
// naive wholesale `entry.VmState = state` in saveVmDeployState would silently ERASE the
// just-registered Ephemeral block on every ordinary Add. saveVmDeployState's own Ephemeral-
// preservation merge (vm_deploy_state.go) is what makes that safe — see its doc comment.
// Scoped to vm only (VmDomainIdentity is vm/libvirt-domain-specific naming) — correct today
// since ephemeral is vm-only (validate_ephemeral.go); pod/k8s pick their OWN key scheme when
// the bed-robustness batch wires their Add/Del paths to this seam.
func ephemeralOverlayKey(deployName string) string {
	return "vm:" + vmshared.VmDomainIdentity(deployName)
}

// ephemeralFallbackNode builds the BundleNode used when dc.Bundle[deployName] has no existing
// per-host-overlay entry (the common first-registration case). FINAL/K5 unit 6a fix (real bug,
// live-bed-caught): a bare spec.BundleNode{} here left Target/From EMPTY — on the next reload,
// deploy_nodeform.go's bundleDiscForEntity sees no target + no pod-workload indicator and
// discriminates the persisted entry as "group", whose closed #GroupInput schema then rejects the
// leftover vm_state field (a hard load failure on every subsequent per-host-overlay read). Seed
// ONLY the identifying fields (Target/From) from the authored node — an overlay entry is STATE,
// never structure, so Children/Members are deliberately NOT copied. This mirrors the
// ALREADY-WORKING charly/vm_deploy_state.go:saveVmDeployState, which sets Target="vm"
// unconditionally on a fresh entry — independent proof dotted deploy identities round-trip
// correctly through dc.Bundle once Target/From are set (the identity itself was never the
// problem). Pulled out as its own function for unit testability — persistEphemeralRuntime itself
// needs a live reverse channel (loadBundleConfig/saveDeployConfig) a standalone test can't drive.
func ephemeralFallbackNode(authored *spec.Deploy) spec.BundleNode {
	node := spec.BundleNode{}
	if authored != nil {
		node.Target = authored.Target
		node.From = authored.From
	}
	return node
}

// ensureEphemeralBundleConfig returns dc with a GUARANTEED non-nil *BundleConfig AND non-nil
// Bundle map. RCA #5 (FINAL/K5 unit 6a, live-probe-caught): a nil *BundleConfig (no overlay file
// at all) was already guarded at persistEphemeralRuntime's call site, but loadBundleConfig can
// ALSO return a non-nil *BundleConfig whose Bundle field is itself nil — the exact shape of a
// genuinely FRESH per-host overlay (a bed's brand-new tmp file, or any operator overlay with no
// `bundle:` section yet decoded from valid-but-bundle-less JSON). Reading dc.Bundle[key] from a
// nil map is safe (ok=false), but persistEphemeralRuntime's !ok branch FABRICATES a fresh entry
// and falls through to a WRITE — unlike clearEphemeralRuntime/bumpParentChildRefcount, which both
// return/continue before ever writing on a nil-map miss (verified safe-by-construction, no fix
// needed there) — so persistEphemeralRuntime is the one writer that needed this: without it,
// `panic: assignment to entry in nil map` on EVERY fresh-overlay registration, previously masked
// because the panic was swallowed by the in-proc plugin dispatch (now made loud —
// recoverEphemeralOpPanic, command.go). Pulled out as its own function purely for testability
// (persistEphemeralRuntime itself needs the seam-coupled loadBundleConfig, not unit-testable
// standalone).
func ensureEphemeralBundleConfig(dc *deploykit.BundleConfig) *deploykit.BundleConfig {
	if dc == nil {
		dc = &deploykit.BundleConfig{}
	}
	if dc.Bundle == nil {
		dc.Bundle = map[string]spec.BundleNode{}
	}
	return dc
}

func persistEphemeralRuntime(authored *spec.Deploy, deployName string, h *ephemeralHandle) error {
	dc, err := loadBundleConfig()
	if err != nil {
		return err
	}
	dc = ensureEphemeralBundleConfig(dc)
	key := ephemeralOverlayKey(deployName)
	node, ok := dc.Bundle[key]
	if !ok {
		node = ephemeralFallbackNode(authored)
	}
	if node.VmState == nil {
		node.VmState = &spec.VmDeployState{}
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
		// from `key`, the dot-sanitized dc.Bundle map key above. teardownChildrenRec reads this
		// back for its recursive `charly bundle del` call, since the map key itself is not
		// reversible to the original address.
		DeployAddress: deployName,
	}
	dc.Bundle[key] = node
	return saveDeployConfig(dc)
}

// clearEphemeralRuntime removes the lifecycle metadata at teardown. Checked against the
// FINAL/K5 unit 6a bed-caught bug (persistEphemeralRuntime's blank-fallback-node →
// kind-misdiscrimination chain): this function does NOT need the same fix — it already
// `return nil`s on `!ok` rather than fabricating a blank node, so it can never write an
// under-specified entry.
func clearEphemeralRuntime(deployName string) error {
	dc, err := loadBundleConfig()
	if err != nil || dc == nil {
		return err
	}
	key := ephemeralOverlayKey(deployName)
	node, ok := dc.Bundle[key]
	if !ok {
		return nil
	}
	if node.VmState == nil || node.VmState.Ephemeral == nil {
		return nil
	}
	node.VmState.Ephemeral = nil
	dc.Bundle[key] = node
	return saveDeployConfig(dc)
}

// bumpParentChildRefcount adjusts the parent ephemeral's child counter by delta (+1 on nested
// register, -1 on nested teardown). Checked against the same bug class as clearEphemeralRuntime:
// it only mutates an entry found by its `range` loop, which already requires
// node.VmState.Ephemeral != nil — it never fabricates a new entry, so it cannot write an
// under-specified one either.
func bumpParentChildRefcount(parentID string, delta int) error {
	dc, err := loadBundleConfig()
	if err != nil || dc == nil {
		return err
	}
	for name, node := range dc.Bundle {
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
		dc.Bundle[name] = node
		return saveDeployConfig(dc)
	}
	return nil
}

// lookupEphemeralByID scans charly.yml for the ephemeral with the given ID. Used for nested TTL
// clipping. The seam-coupled LOAD (loadBundleConfig) is not unit-testable standalone; the pure
// scan is split into ephemeralByIDFromBundleConfig (ephemeral_test.go tests that directly).
func lookupEphemeralByID(id string) (*spec.EphemeralRuntime, error) {
	dc, err := loadBundleConfig()
	if err != nil || dc == nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	return ephemeralByIDFromBundleConfig(dc, id)
}

// ephemeralByIDFromBundleConfig is the pure scan lookupEphemeralByID applies once it has an
// already-loaded BundleConfig.
func ephemeralByIDFromBundleConfig(dc *deploykit.BundleConfig, id string) (*spec.EphemeralRuntime, error) {
	for _, node := range dc.Bundle {
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
	dc, err := loadBundleConfig()
	if err != nil || dc == nil {
		return err
	}
	key := ephemeralOverlayKey(deployName)
	parentID := ""
	if node, ok := dc.Bundle[key]; ok && node.VmState != nil && node.VmState.Ephemeral != nil {
		parentID = node.VmState.Ephemeral.ID
	}
	if parentID == "" {
		return nil
	}
	// Seed visited with OUR OWN dc.Bundle key (the sanitized form) — teardownChildrenRec's cycle
	// guard compares against the map's native (already-sanitized) keys, never the raw deployName.
	visited := map[string]bool{key: true}
	return teardownChildrenRec(dc, parentID, visited)
}

func teardownChildrenRec(dc *deploykit.BundleConfig, parentID string, visited map[string]bool) error {
	var toDel []string
	for name, node := range dc.Bundle {
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
		// dc.Bundle map key `name` itself — the key is a dot-sanitized "vm:<domain-id>" form
		// that `charly bundle del` cannot resolve back to the original (possibly dotted) deploy
		// tree address. Falls back to `name` only for a pre-fix entry that predates this field
		// (best-effort — such an entry is already a latent leak from before this cutover).
		delTarget := name
		if node, ok := dc.Bundle[name]; ok && node.VmState != nil && node.VmState.Ephemeral != nil {
			if node.VmState.Ephemeral.DeployAddress != "" {
				delTarget = node.VmState.Ephemeral.DeployAddress
			}
			if err := teardownChildrenRec(dc, node.VmState.Ephemeral.ID, visited); err != nil {
				return err
			}
		}
		// Invoke `charly bundle del <child> --assume-yes` — shelling out so the child's full
		// cleanup (including its own teardownEphemeral) runs.
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := exec.Command(exe, ephemeralDeployDelArgv(delTarget)...)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: nested teardown of %q failed: %v\n", delTarget, err)
		}
	}
	return nil
}
