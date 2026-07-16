package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/opencharly/sdk/spec"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/opencharly/sdk/deploykit"
)

// ephemeral_lifecycle.go — shared lifecycle helpers for ephemeral
// deployments. All three target types (vm/pod/k8s) call into these
// functions from the existing run* paths in deploy_add_cmd_*.go and
// deploy_del_cmd*.go. The helper logic is target-agnostic; per-target
// instantiation/destruction stays in the target's own runner.
//
// Two main entry points:
//
//   RegisterEphemeralLifecycle(node, deployName) (*EphemeralRuntime, error)
//       Called as the FIRST action of run* before any target work.
//       Registers a systemd transient timer, increments snapshot
//       refcount (vm-target only), populates charly.yml's vm_state /
//       pod_state / k8s_state Ephemeral block, returns a runtime
//       handle. The timer-first ordering is panic-safe: even if the
//       caller crashes mid-provisioning, the timer fires `charly bundle
//       del <name> --assume-yes` after the TTL.
//
//   TeardownEphemeralLifecycle(node, deployName, handle) error
//       Called as the LAST action of run* after teardown completes.
//       Recursively dels nested children depth-first, cancels the
//       transient timer, decrements snapshot refcount, removes
//       charly.yml lifecycle metadata.
//
// State persistence: lifecycle metadata lives in
// charly.yml.deployment.<name>.vm_state.ephemeral (or pod_state /
// k8s_state when those blocks exist). Symmetric across targets — the
// helper writes through a target-agnostic path.

// EphemeralHandle captures the runtime state returned by Register and
// consumed by Teardown. Mirrors EphemeralRuntime but with parsed types
// (time.Time deadlines, etc.) for the helper's own use.
type EphemeralHandle struct {
	// ID is the six-char random hex identifier.
	ID string

	// DeployName is the charly.yml entry name.
	DeployName string

	// InstanceName is the rendered NamingPattern result.
	InstanceName string

	// TimerUnit is the systemd transient unit registered for TTL
	// safety. Empty if registration failed.
	TimerUnit string

	// TtlDeadline is the absolute time the transient timer fires.
	TtlDeadline time.Time

	// ParentVm names the kind:vm entity (or kind:box / kind:k8s for
	// pod / k8s targets). Empty for non-clone deploys.
	ParentVm string

	// ParentSnapshot names the snapshot used as the cloned overlay's
	// backing disk, when applicable.
	ParentSnapshot string

	// ParentEphemeral, when non-empty, is the ID of the outer
	// ephemeral that wraps this one (nested case).
	ParentEphemeral string
}

// RegisterEphemeralLifecycle is the entry point invoked at the start
// of a deploy add for an ephemeral resource. Performs (in order):
//  1. Generate unique instance ID (six-char hex).
//  2. Resolve parent ephemeral from CHARLY_EPHEMERAL_PARENT environment
//     variable (nested-case detection).
//  3. Compute effective TTL (clipped to parent's remaining TTL when
//     nested).
//  4. Register systemd transient timer that runs `charly bundle del
//     <deployName> --assume-yes` after the TTL.
//  5. Increment vm-target parent-snapshot refcount when applicable.
//  6. Persist EphemeralRuntime into charly.yml's vm_state.ephemeral
//     (or pod_state / k8s_state for those targets).
//
// Returns the handle that should be passed to TeardownEphemeralLifecycle
// at deploy del time.
func RegisterEphemeralLifecycle(node *BundleNode, deployName string) (*EphemeralHandle, error) {
	if node == nil || !node.IsEphemeral() {
		return nil, fmt.Errorf("RegisterEphemeralLifecycle: node %q is not marked ephemeral", deployName)
	}

	id, err := newEphemeralID()
	if err != nil {
		return nil, fmt.Errorf("generating ephemeral id: %w", err)
	}

	parentEph := os.Getenv("CHARLY_EPHEMERAL_PARENT")
	ttl, err := effectiveTTL(node, parentEph)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(ttl)

	pattern := node.Ephemeral.EffectiveNamingPattern()
	instanceName, err := renderNamingPattern(pattern, deployName, id)
	if err != nil {
		return nil, fmt.Errorf("rendering naming_pattern %q: %w", pattern, err)
	}

	// Step 4: register the transient timer FIRST. Panic-safe ordering —
	// we want the timer in place even if subsequent steps blow up.
	timerUnit, err := registerTransientTimer(deployName, ttl)
	if err != nil {
		// Registration failure is logged but doesn't abort the deploy
		// — falling back to foreground-handler-only is degraded but
		// usable on systems without user systemd.
		fmt.Fprintf(os.Stderr, "warning: registering TTL transient timer: %v (continuing without TTL safety net)\n", err)
		timerUnit = ""
	}

	handle := &EphemeralHandle{
		ID:              id,
		DeployName:      deployName,
		InstanceName:    instanceName,
		TimerUnit:       timerUnit,
		TtlDeadline:     deadline,
		ParentEphemeral: parentEph,
	}

	// Step 5: vm-target snapshot refcount.
	if nodeTraits(node).Venue == "ssh" && node.From != "" && node.FromSnapshot != "" { // vm (ssh venue)
		if err := IncrementSnapshotRefcount(node.From, node.FromSnapshot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: incrementing snapshot refcount: %v\n", err)
		}
		handle.ParentVm = node.From
		handle.ParentSnapshot = node.FromSnapshot
	}

	// Step 6: persist EphemeralRuntime into charly.yml.
	if err := persistEphemeralRuntime(deployName, handle); err != nil {
		fmt.Fprintf(os.Stderr, "warning: persisting ephemeral runtime: %v\n", err)
	}

	// Increment parent's child-refcount when nested.
	if parentEph != "" {
		_ = bumpParentChildRefcount(parentEph, +1)
	}

	return handle, nil
}

// TeardownEphemeralLifecycle is the entry point invoked at the end of
// a deploy del for an ephemeral resource. Performs (in order):
//  1. Recursively del nested children depth-first.
//  2. Cancel the systemd transient timer.
//  3. Decrement snapshot refcount (vm-target only).
//  4. Decrement parent's child-refcount (nested case).
//  5. Clear EphemeralRuntime from charly.yml.
func TeardownEphemeralLifecycle(node *BundleNode, deployName string) error {
	if node == nil || !node.IsEphemeral() {
		return fmt.Errorf("TeardownEphemeralLifecycle: node %q is not marked ephemeral", deployName)
	}

	// Step 1: recursive child teardown via registry scan.
	if err := teardownChildren(deployName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: nested ephemeral teardown: %v\n", err)
	}

	// Step 2: cancel transient timer.
	if node.VmState != nil && node.VmState.Ephemeral != nil && node.VmState.Ephemeral.TimerUnit != "" {
		cancelTransientTimer(node.VmState.Ephemeral.TimerUnit)
	}

	// Step 3: snapshot refcount decrement (vm-target).
	if nodeTraits(node).Venue == "ssh" && node.From != "" && node.FromSnapshot != "" { // vm (ssh venue)
		if err := DecrementSnapshotRefcount(node.From, node.FromSnapshot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: decrementing snapshot refcount: %v\n", err)
		}
	}

	// Step 4: parent's child-refcount decrement (nested case).
	if node.VmState != nil && node.VmState.Ephemeral != nil && node.VmState.Ephemeral.ParentEphemeral != "" {
		_ = bumpParentChildRefcount(node.VmState.Ephemeral.ParentEphemeral, -1)
	}

	// Step 5: clear EphemeralRuntime from charly.yml.
	if err := clearEphemeralRuntime(deployName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: clearing ephemeral runtime: %v\n", err)
	}
	return nil
}

// effectiveTTL computes the TTL for a deploy, clipping to the parent
// ephemeral's remaining TTL when nested. parentID may be empty.
func effectiveTTL(node *BundleNode, parentID string) (time.Duration, error) {
	declared := node.Ephemeral.EffectiveTTL()
	if parentID == "" {
		return declared, nil
	}
	parent, err := lookupEphemeralByID(parentID)
	if err != nil {
		// Parent gone or unknown — proceed with declared TTL but warn.
		fmt.Fprintf(os.Stderr, "warning: parent ephemeral %q not found; using declared TTL %s\n", parentID, declared)
		return declared, nil
	}
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

// renderNamingPattern fills in {{.Source}} and {{.UUID6}} variables.
func renderNamingPattern(pattern, source, id string) (string, error) {
	t, err := template.New("naming").Parse(pattern)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, struct {
		Source string
		UUID6  string
	}{Source: source, UUID6: id})
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// newEphemeralID returns six characters of cryptographically-strong
// random hex. Six characters is 24 bits of entropy — enough to make
// concurrent collisions vanishingly rare for a per-deploy lifecycle.
func newEphemeralID() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// registerTransientTimer creates a systemd-run --user --on-active=<ttl>
// transient unit that fires `charly bundle del <deployName> --assume-yes` when
// the TTL elapses. Returns the unit name (suitable for cancel).
//
// Falls back to a no-op when systemd-run is not available (best-effort
// safety net; foreground signal handler is the fast path anyway).
func registerTransientTimer(deployName string, ttl time.Duration) (string, error) {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return "", fmt.Errorf("systemd-run not in PATH; TTL safety net disabled")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating charly binary: %w", err)
	}
	unitName := fmt.Sprintf("charly-bundle-del-%s-%d", sanitizeUnitName(deployName), time.Now().Unix())
	args := append([]string{
		"--user",
		"--unit=" + unitName,
		"--on-active=" + ttl.String(),
		exe,
	}, deployDelArgv(deployName)...)
	cmd := exec.Command("systemd-run", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("systemd-run: %w", err)
	}
	return unitName + ".timer", nil
}

// cancelTransientTimer stops a previously registered transient unit.
// Best-effort — failures are logged but not surfaced.
func cancelTransientTimer(unit string) {
	if unit == "" {
		return
	}
	cmd := exec.Command("systemctl", "--user", "stop", unit)
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

// sanitizeUnitName makes a string safe for systemd unit naming
// (replaces / and . with -).
func sanitizeUnitName(s string) string {
	r := strings.ReplaceAll(s, "/", "-")
	r = strings.ReplaceAll(r, ".", "-")
	return r
}

// persistEphemeralRuntime writes the EphemeralHandle into charly.yml's
// vm_state.ephemeral (or pod_state / k8s_state for those targets).
func persistEphemeralRuntime(deployName string, h *EphemeralHandle) error {
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		return err
	}
	if dc == nil {
		dc = &BundleConfig{Bundle: map[string]BundleNode{}}
	}
	node, ok := dc.Bundle[deployName]
	if !ok {
		node = BundleNode{}
	}
	if node.VmState == nil {
		node.VmState = &spec.VmDeployState{}
	}
	node.VmState.Ephemeral = &EphemeralRuntime{
		ID:              h.ID,
		ParentVm:        h.ParentVm,
		ParentSnapshot:  h.ParentSnapshot,
		ParentEphemeral: h.ParentEphemeral,
		TimerUnit:       h.TimerUnit,
		TtlDeadline:     h.TtlDeadline.Format(time.RFC3339),
		Status:          "active",
		InstanceName:    h.InstanceName,
	}
	dc.Bundle[deployName] = node
	return saveBundleConfigNodeForm(dc)
}

// clearEphemeralRuntime removes the lifecycle metadata at teardown.
func clearEphemeralRuntime(deployName string) error {
	dc, err := deploykit.LoadBundleConfig()
	if err != nil || dc == nil {
		return err
	}
	node, ok := dc.Bundle[deployName]
	if !ok {
		return nil
	}
	if node.VmState == nil || node.VmState.Ephemeral == nil {
		return nil
	}
	node.VmState.Ephemeral = nil
	dc.Bundle[deployName] = node
	return saveBundleConfigNodeForm(dc)
}

// bumpParentChildRefcount adjusts the parent ephemeral's child counter
// by delta (+1 on nested register, -1 on nested teardown).
func bumpParentChildRefcount(parentID string, delta int) error {
	dc, err := deploykit.LoadBundleConfig()
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
		return saveBundleConfigNodeForm(dc)
	}
	return nil
}

// lookupEphemeralByID scans charly.yml for the ephemeral with the
// given ID. Used for nested TTL clipping.
func lookupEphemeralByID(id string) (*EphemeralRuntime, error) {
	dc, err := deploykit.LoadBundleConfig()
	if err != nil || dc == nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
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

// teardownChildren recursively dels nested ephemerals whose parent is
// the deploy with the given name's ephemeral ID. Depth-first; visited-
// set guards against cycles (which would only occur via manual
// charly.yml editing).
func teardownChildren(deployName string) error {
	dc, err := deploykit.LoadBundleConfig()
	if err != nil || dc == nil {
		return err
	}
	parentID := ""
	if node, ok := dc.Bundle[deployName]; ok && node.VmState != nil && node.VmState.Ephemeral != nil {
		parentID = node.VmState.Ephemeral.ID
	}
	if parentID == "" {
		return nil
	}
	visited := map[string]bool{deployName: true}
	return teardownChildrenRec(dc, parentID, visited)
}

func teardownChildrenRec(dc *BundleConfig, parentID string, visited map[string]bool) error {
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
		// Recurse into the child first (depth-first).
		if node, ok := dc.Bundle[name]; ok && node.VmState != nil && node.VmState.Ephemeral != nil {
			if err := teardownChildrenRec(dc, node.VmState.Ephemeral.ID, visited); err != nil {
				return err
			}
		}
		// Invoke `charly bundle del <child> --assume-yes`. We shell out so the
		// child's full cleanup logic (including its own
		// TeardownEphemeralLifecycle) runs.
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := exec.Command(exe, deployDelArgv(name)...)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: nested teardown of %q failed: %v\n", name, err)
		}
	}
	return nil
}
