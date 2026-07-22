package preempt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// arbiter.go — the RESOURCE ARBITER (cutover C9), moved OUT of charly core (the former
// charly/preempt.go ResourceArbiter) into this COMPILED-IN plugin. It owns the coordination
// LOGIC: acquire/release exclusive+shared leases, stop + restore holders, the crash-safe lease
// ledger, GPU-resource poisoning, owner liveness, and the driver-mode arbitration math. Its host
// DEPENDENCIES (project config, VM/pod lifecycle, the GPU driver flip) it CANNOT hold across the
// module boundary — it reaches them mid-logic over the ExecutorService.HostArbiter reverse
// channel (the 8 host seams: gather/resources/running/stop[+wait]/start/switchMode/ensureCDI/gpuCDI).
//
// The 9 seam fields on ResourceArbiter are INJECTABLE so the unit suite fakes them (the
// seam-faked tests relocated from charly/preempt_test.go); newArbiter wires the production seams
// to the host over the reverse channel. Crash-safety is unchanged: the ledger is written BEFORE
// any holder is stopped, and restore = "start every listed holder that isn't running".

// ResourceArbiter coordinates exclusive host-resource access. The seams are injected so unit
// tests fake holder discovery + lifecycle without a live host; newArbiter wires them to the
// HostArbiter reverse channel.
type ResourceArbiter struct {
	ledgerPath string
	gather     func() []spec.HolderDescriptor          // candidate preemptible holders (host-projected)
	running    func(addr spec.HolderAddr) bool         // is this deployment running?
	stop       func(addr spec.HolderAddr) error        // graceful stop + WAIT until stopped (host folds the wait)
	start      func(addr spec.HolderAddr) error        // start an already-configured deployment
	resources  func() map[string]string                // gpu-backed token -> PCI vendor (arbitration-only omitted)
	switchMode func(vendor, mode string) (bool, error) // flip a gpu-backed token's driver mode; wedged bool for poisoning
	ensureCDI  func()                                  // regenerate the nvidia CDI spec after a flip to nvidia
	gpuCDI     func() []spec.HolderAddr                // running charly GPU-CDI pods (reclaim before a vfio flip)
	nowUTC     func() string
}

// newArbiter wires the production seams. `gather`/`resources` stay on the HostArbiter reverse
// channel — the ONLY 2 of the original 8 host seams that are genuinely K1-blocked (LoadUnified
// project-config coupled); the other 6 (running/stop/start/switchMode/ensureCDI/gpuCDI) are
// FLOOR-SLIM-proper Unit-8's MOVE — they run directly in this plugin (holder_dispatch.go), using
// `exec` only for the class-agnostic InvokeProvider dispatch to the libvirt/gpu plugins, never a
// host callback.
func newArbiter(ctx context.Context, exec *sdk.Executor) *ResourceArbiter {
	return &ResourceArbiter{
		ledgerPath: preemptLedgerPath(),
		gather:     func() []spec.HolderDescriptor { return hostGather(ctx, exec) },
		running:    func(a spec.HolderAddr) bool { return pluginHolderRunning(ctx, exec, a) },
		stop:       func(a spec.HolderAddr) error { return pluginHolderStop(ctx, exec, a) },
		start:      func(a spec.HolderAddr) error { return pluginHolderStart(ctx, exec, a) },
		resources:  func() map[string]string { return hostResources(ctx, exec) },
		switchMode: func(v, m string) (bool, error) { return pluginSwitchMode(ctx, exec, v, m) },
		ensureCDI:  func() { pluginEnsureCDI(ctx, exec) },
		gpuCDI:     func() []spec.HolderAddr { return pluginGpuCDIHolders() },
		nowUTC:     func() string { return time.Now().UTC().Format(time.RFC3339) },
	}
}

// --- the verb:arbiter Invoke handler (action-multiplexed) ------------------------------------

// invokeArbiter runs one arbiter action (the in-core proxy's spec.ArbiterInvokeInput) against a
// live arbiter wired to the host reverse channel, and returns the matching reply.
func invokeArbiter(ctx context.Context, exec *sdk.Executor, in spec.ArbiterInvokeInput) spec.ArbiterInvokeReply {
	a := newArbiter(ctx, exec)
	switch in.Action {
	case spec.ArbiterActionAcquireExclusive:
		active, err := a.AcquireExclusive(in.Claimant, in.Tokens, in.ClaimAddr, in.Transient)
		return spec.ArbiterInvokeReply{Active: active, Error: errStr(err)}
	case spec.ArbiterActionAcquireShared:
		active, err := a.AcquireShared(in.Claimant, in.Tokens, in.ClaimAddr, in.Transient)
		return spec.ArbiterInvokeReply{Active: active, Error: errStr(err)}
	case spec.ArbiterActionRelease:
		return spec.ArbiterInvokeReply{Error: errStr(a.ReleaseClaimant(in.Claimant, in.Success))}
	case spec.ArbiterActionStatus:
		ledger, stranded, err := a.Status()
		return spec.ArbiterInvokeReply{Ledger: ledger, Stranded: stranded, Error: errStr(err)}
	case spec.ArbiterActionReconcile:
		return spec.ArbiterInvokeReply{Error: errStr(a.reconcileStranded())}
	case spec.ArbiterActionClearPoison:
		a.clearPoison(in.Token)
		return spec.ArbiterInvokeReply{}
	case spec.ArbiterActionResourcePoisoned:
		return spec.ArbiterInvokeReply{Bool: a.resourcePoisoned(in.Token)}
	default:
		return spec.ArbiterInvokeReply{Error: fmt.Sprintf("unknown arbiter action %q", in.Action)}
	}
}

// --- host-seam calls over the reverse channel ------------------------------------------------

func hostGather(ctx context.Context, exec *sdk.Executor) []spec.HolderDescriptor {
	out, err := exec.HostArbiter(ctx, spec.ArbiterSeamGather, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "preempt: gather seam: %v\n", err)
		return nil
	}
	var r spec.ArbiterGatherReply
	_ = json.Unmarshal(out, &r)
	return r.Holders
}

func hostResources(ctx context.Context, exec *sdk.Executor) map[string]string {
	out, err := exec.HostArbiter(ctx, spec.ArbiterSeamResources, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "preempt: resources seam: %v\n", err)
		return nil
	}
	var r spec.ArbiterResourcesReply
	_ = json.Unmarshal(out, &r)
	return r.Gpu
}

// --- acquire -----------------------------------------------------------------------------------

// AcquireExclusive gives a claimant SOLE use of its tokens: it stops every running preemptible
// holder AND every running SHARED-claim pod of them, flips each gpu-backed token to vfio, and
// persists a crash-safe lease. Returns active=true when a lease is held. tokens/claimAddr are
// pre-computed host-side by the in-core shim.
func (a *ResourceArbiter) AcquireExclusive(claimant string, tokens []string, claimAddr spec.HolderAddr, transient bool) (bool, error) {
	tokens = dedupeNonEmpty(tokens)
	if len(tokens) == 0 {
		return false, nil
	}
	unlock, lerr := a.acquireArbiterLock()
	if lerr != nil {
		return false, fmt.Errorf("acquiring resource-arbiter lock: %w", lerr)
	}
	defer func() { _ = unlock() }()

	if err := a.reconcileStranded(); err != nil {
		return false, err
	}
	ledger, err := a.loadLedger()
	if err != nil {
		return false, err
	}
	for _, lz := range ledger.Leases {
		if lz.Claimant == claimant {
			return true, nil // idempotent re-acquire
		}
	}
	for _, lz := range ledger.Leases {
		if lz.Shared {
			continue
		}
		if shared := intersect(lz.Tokens, tokens); len(shared) > 0 {
			return false, fmt.Errorf(
				"exclusive resource %s is already claimed by %q — release it (`charly preempt restore %s`) before claiming it for %q",
				strings.Join(shared, ", "), lz.Claimant, lz.Claimant, claimant)
		}
	}
	if tok := a.firstPoisonedToken(tokens); tok != "" {
		return false, fmt.Errorf("GPU resource %q is unavailable until a host reboot — a previous driver switch wedged the card's device_lock (see `charly vm gpu status` / `charly vm gpu recover`); reboot clears it", tok)
	}

	toStop := a.holdersToStop(tokens, claimant)
	var kept []spec.PreemptLease
	var sharedPodStops []spec.PreemptedHolder
	for _, lz := range ledger.Leases {
		if !lz.Shared || len(intersect(lz.Tokens, tokens)) == 0 {
			kept = append(kept, lz)
			continue
		}
		if a.running(lz.Claim) {
			sharedPodStops = append(sharedPodStops, spec.PreemptedHolder{
				Addr:    lz.Claim,
				Holds:   intersect(lz.Tokens, tokens),
				Restore: spec.PreemptRestoreAlways,
			})
		}
		toStop = append(toStop, lz.Preempted...) // carry the operator-VM restore obligation forward
	}
	toStop = dedupePreempted(toStop)
	ledger.Leases = kept

	lease := spec.PreemptLease{
		Claimant:   claimant,
		Claim:      claimAddr,
		Tokens:     tokens,
		Shared:     false,
		Mode:       spec.GpuModeVfio,
		Transient:  transient,
		Preempted:  toStop,
		Created:    a.nowUTC(),
		OwnerPID:   int64(os.Getpid()),
		OwnerStart: selfProcStart(),
	}
	ledger.Leases = append(ledger.Leases, lease)
	if err := a.saveLedger(ledger); err != nil {
		return false, err
	}

	allStops := append(append([]spec.PreemptedHolder{}, lease.Preempted...), sharedPodStops...)
	if err := a.stopHolders(allStops, claimant); err != nil {
		return false, err
	}
	// Reclaim any leaked/kept charly GPU pod still holding the card (a stray with no active lease) —
	// otherwise its open nvidia handle wedges the nvidia->vfio flip below (ledger.Leases now holds
	// this new exclusive lease + the kept non-overlapping ones, so a legit claimant is never touched).
	a.reclaimStrayGPUHolders(ledger.Leases)
	if err := a.applyMode(tokens, spec.GpuModeVfio); err != nil {
		a.restoreHolders(lease.Preempted)
		_ = a.removeLease(claimant)
		return false, fmt.Errorf("freeing %s for vfio passthrough: %w", strings.Join(tokens, ", "), err)
	}
	return true, nil
}

// AcquireShared brings up a SHARED (refcounted) claim. The FIRST shared claim flips the token's
// gpu-backed resource to nvidia (+ regenerate CDI) and preempts any running preemptible holder;
// subsequent claims just refcount. Refused only when an EXCLUSIVE claim already holds the token.
func (a *ResourceArbiter) AcquireShared(claimant string, tokens []string, claimAddr spec.HolderAddr, transient bool) (bool, error) {
	tokens = dedupeNonEmpty(tokens)
	if len(tokens) == 0 {
		return false, nil
	}
	unlock, lerr := a.acquireArbiterLock()
	if lerr != nil {
		return false, fmt.Errorf("acquiring resource-arbiter lock: %w", lerr)
	}
	defer func() { _ = unlock() }()

	if err := a.reconcileStranded(); err != nil {
		return false, err
	}
	ledger, err := a.loadLedger()
	if err != nil {
		return false, err
	}
	for _, lz := range ledger.Leases {
		if lz.Claimant == claimant {
			return true, nil
		}
	}
	for _, lz := range ledger.Leases {
		if lz.Shared {
			continue
		}
		if s := intersect(lz.Tokens, tokens); len(s) > 0 {
			return false, fmt.Errorf(
				"resource %s is held EXCLUSIVELY by %q — cannot share it for %q (release the exclusive claim first)",
				strings.Join(s, ", "), lz.Claimant, claimant)
		}
	}
	if tok := a.firstPoisonedToken(tokens); tok != "" {
		return false, fmt.Errorf("GPU resource %q is unavailable until a host reboot — a previous driver switch wedged the card's device_lock (see `charly vm gpu status` / `charly vm gpu recover`); reboot clears it", tok)
	}

	var toStop []spec.PreemptedHolder
	if !tokenHeldByShared(ledger, tokens) {
		toStop = a.holdersToStop(tokens, claimant)
	}

	lease := spec.PreemptLease{
		Claimant:   claimant,
		Claim:      claimAddr,
		Tokens:     tokens,
		Shared:     true,
		Mode:       spec.GpuModeNvidia,
		Transient:  transient,
		Preempted:  toStop,
		Created:    a.nowUTC(),
		OwnerPID:   int64(os.Getpid()),
		OwnerStart: selfProcStart(),
	}
	ledger.Leases = append(ledger.Leases, lease)
	if err := a.saveLedger(ledger); err != nil {
		return false, err
	}
	if err := a.stopHolders(toStop, claimant); err != nil {
		return false, err
	}
	if err := a.applyMode(tokens, spec.GpuModeNvidia); err != nil {
		a.restoreHolders(toStop)
		_ = a.removeLease(claimant)
		return false, fmt.Errorf("setting %s to nvidia/CDI mode for %q: %w", strings.Join(tokens, ", "), claimant, err)
	}
	return true, nil
}

// --- stop / restore / apply-mode --------------------------------------------------------------

// holdersToStop selects the RUNNING preemptible holders whose holds intersect tokens (excluding
// the claimant). Operates on the host-projected descriptors (holds/addr/restore already resolved).
func (a *ResourceArbiter) holdersToStop(tokens []string, claimant string) []spec.PreemptedHolder {
	var toStop []spec.PreemptedHolder
	for _, desc := range a.gather() {
		if desc.Name == claimant {
			continue
		}
		shared := intersect(desc.Holds, tokens)
		if len(shared) == 0 {
			continue
		}
		if !a.running(desc.Addr) {
			continue
		}
		toStop = append(toStop, spec.PreemptedHolder{Addr: desc.Addr, Holds: shared, Restore: desc.Restore})
	}
	return toStop
}

// stopHolders stops each holder (the host seam folds the wait-until-stopped) and rolls back on
// any failure — restarting what it already stopped and dropping the claimant's lease.
func (a *ResourceArbiter) stopHolders(toStop []spec.PreemptedHolder, claimant string) error {
	for i, ph := range toStop {
		fmt.Fprintf(os.Stderr, "preempt: stopping holder %q to free %s for %q\n",
			ph.Addr.Name, strings.Join(ph.Holds, ", "), claimant)
		if stopErr := a.stop(ph.Addr); stopErr != nil {
			for _, done := range toStop[:i] {
				if !a.running(done.Addr) {
					_ = a.start(done.Addr)
				}
			}
			_ = a.removeLease(claimant)
			return fmt.Errorf("preempting holder %q: %w", ph.Addr.Name, stopErr)
		}
		fmt.Fprintf(os.Stderr, "preempt: holder %q stopped — %s freed for %q\n",
			ph.Addr.Name, strings.Join(ph.Holds, ", "), claimant)
	}
	return nil
}

// restoreHolders starts every holder that isn't currently running. Idempotent.
func (a *ResourceArbiter) restoreHolders(holders []spec.PreemptedHolder) bool {
	allUp := true
	for _, ph := range holders {
		if a.running(ph.Addr) {
			continue
		}
		if err := a.start(ph.Addr); err != nil {
			fmt.Fprintf(os.Stderr, "preempt: could not restore holder %q: %v\n", ph.Addr.Name, err)
			allUp = false
			continue
		}
		fmt.Fprintf(os.Stderr, "preempt: restored holder %q\n", ph.Addr.Name)
	}
	return allUp
}

// reclaimStrayGPUHolders stops every RUNNING charly GPU-CDI pod that is NOT covered by an active
// lease in leases — a leaked/kept eval bed the ledger lost track of (its transient lease was
// owner-PID-tied to a check-runner process that has since exited, while the container kept running
// with an open nvidia_uvm handle). Called immediately before any nvidia->vfio flip so a stray handle
// cannot wedge `modprobe -r nvidia` (the old "external GPU client (could not be identified)" refusal
// that left a leaked bed holding the card indefinitely). Reclaimed pods are gracefully stopped
// (freeing the module refcount) and deliberately NOT restored: a disposable bed left running past its
// lease is leaked state, never a holder to bring back.
func (a *ResourceArbiter) reclaimStrayGPUHolders(leases []spec.PreemptLease) {
	holders := a.gpuCDI()
	if len(holders) == 0 {
		return
	}
	leased := map[string]bool{}
	for _, lz := range leases {
		leased[lz.Claim.Name] = true
	}
	for _, addr := range holders {
		if leased[addr.Name] || !a.running(addr) {
			continue
		}
		fmt.Fprintf(os.Stderr, "preempt: reclaiming stray GPU holder %q (a running charly GPU pod with no active lease) to free the card\n", addr.Name)
		if err := a.stop(addr); err != nil {
			fmt.Fprintf(os.Stderr, "preempt: could not reclaim stray GPU holder %q: %v\n", addr.Name, err)
		}
	}
}

// applyMode flips every gpu-backed token in tokens to the target driver MODE (+ regenerate CDI
// after a flip to nvidia). Arbitration-only tokens (absent from the resources map) are skipped —
// there is no device to flip (the selector-less-token path the C9 R10 exercises with ZERO GPU).
func (a *ResourceArbiter) applyMode(tokens []string, mode string) error {
	resources := a.resources()
	for _, tok := range tokens {
		vendor, ok := resources[tok]
		if !ok {
			continue // arbitration-only token — no physical device to rebind
		}
		if a.resourcePoisoned(tok) {
			return fmt.Errorf("resource %q is poisoned: a previous GPU driver switch wedged the device_lock — a host reboot is required (%w)", tok, spec.ErrGPUSwitchWedged)
		}
		wedged, err := a.switchMode(vendor, mode)
		if err != nil {
			if wedged {
				a.poisonResource(tok) // contain the cascade: no later claimant may re-wedge
			}
			return fmt.Errorf("setting resource %q to %s mode: %w", tok, mode, err)
		}
		if mode == spec.GpuModeNvidia {
			a.ensureCDI()
		}
	}
	return nil
}

// --- release / reconcile ----------------------------------------------------------------------

// ReleaseClaimant restores the holders a claimant's lease stopped and removes the lease.
func (a *ResourceArbiter) ReleaseClaimant(claimant string, success bool) error {
	unlock, lerr := a.acquireArbiterLock()
	if lerr != nil {
		return fmt.Errorf("acquiring resource-arbiter lock: %w", lerr)
	}
	defer func() { _ = unlock() }()

	ledger, err := a.loadLedger()
	if err != nil {
		return err
	}
	idx := -1
	for i, lz := range ledger.Leases {
		if lz.Claimant == claimant {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	lease := ledger.Leases[idx]
	remaining := make([]spec.PreemptLease, 0, len(ledger.Leases)-1)
	remaining = append(remaining, ledger.Leases[:idx]...)
	remaining = append(remaining, ledger.Leases[idx+1:]...)

	if !a.releaseLeaseEffects(lease, remaining, success) {
		return fmt.Errorf("could not restore all holders for %q — lease retained; retry with `charly preempt restore %s`", claimant, claimant)
	}
	ledger.Leases = remaining
	return a.saveLedger(ledger)
}

// releaseLeaseEffects applies the side-effects of removing lease from a ledger whose post-removal
// state is remaining: restore holders whose token is now FREE, carry forward those still claimed
// by a survivor, and recompute+apply each touched token's driver mode. Returns false (retain the
// lease) on a PARTIAL holder restore. Shared by ReleaseClaimant + reconcileStranded (R3).
func (a *ResourceArbiter) releaseLeaseEffects(lease spec.PreemptLease, remaining []spec.PreemptLease, success bool) bool {
	var toRestore, carry []spec.PreemptedHolder
	for _, ph := range lease.Preempted {
		if !success && ph.Restore == spec.PreemptRestoreSuccess {
			fmt.Fprintf(os.Stderr,
				"preempt: leaving holder %q stopped (restore: on-success, claim failed) — `charly preempt restore %s` to bring it back\n",
				ph.Addr.Name, lease.Claimant)
			continue
		}
		if tokenClaimed(remaining, ph.Holds) {
			carry = append(carry, ph)
		} else {
			toRestore = append(toRestore, ph)
		}
	}
	if !a.restoreHolders(toRestore) {
		return false
	}
	if len(carry) > 0 {
		attachPreemptedToSurvivor(remaining, carry)
	}
	for _, tok := range lease.Tokens {
		mode := desiredModeForToken(remaining, tok)
		if mode == spec.GpuModeVfio {
			// The last claim on tok is gone; reclaim any stray charly GPU pod still holding the card
			// (a leaked bed whose lease died with its check-runner) so the nvidia->vfio flip-back
			// can't wedge on its open handle. Idempotent; `remaining` has no live claim on tok.
			a.reclaimStrayGPUHolders(remaining)
		}
		if _, err := a.switchModeForToken(tok, mode); err != nil {
			fmt.Fprintf(os.Stderr, "preempt: could not set %q to %s mode after releasing %q: %v\n", tok, mode, lease.Claimant, err)
		}
	}
	return true
}

// switchModeForToken flips a single token's gpu-backed device (or no-ops for an arbitration-only
// token), mirroring applyMode's per-token step for the release path.
func (a *ResourceArbiter) switchModeForToken(tok, mode string) (bool, error) {
	vendor, ok := a.resources()[tok]
	if !ok {
		return false, nil
	}
	return a.switchMode(vendor, mode)
}

// reconcileStranded restores holders for any lease whose OWNER is gone (leaseLive false), then
// drops the fully-restored lease. Conservative: a live-owner lease is left untouched.
func (a *ResourceArbiter) reconcileStranded() error {
	ledger, err := a.loadLedger()
	if err != nil {
		return err
	}
	changed := false
	for i := 0; i < len(ledger.Leases); {
		lz := ledger.Leases[i]
		if a.leaseLive(lz) {
			i++
			continue
		}
		remaining := make([]spec.PreemptLease, 0, len(ledger.Leases)-1)
		remaining = append(remaining, ledger.Leases[:i]...)
		remaining = append(remaining, ledger.Leases[i+1:]...)
		if !a.releaseLeaseEffects(lz, remaining, true) {
			i++ // partial restore → retain, retry later
			continue
		}
		fmt.Fprintf(os.Stderr, "preempt: reconciled stranded lease (claimant %q gone) — holders restored\n", lz.Claimant)
		ledger.Leases = remaining
		changed = true
	}
	if changed {
		return a.saveLedger(ledger)
	}
	return nil
}

// Status returns the current ledger plus the stranded-claimant names.
func (a *ResourceArbiter) Status() (*spec.PreemptLedger, []string, error) {
	ledger, err := a.loadLedger()
	if err != nil {
		return nil, nil, err
	}
	var stranded []string
	for _, lz := range ledger.Leases {
		if !a.leaseLive(lz) {
			stranded = append(stranded, lz.Claimant)
		}
	}
	return ledger, stranded, nil
}

// leaseLive reports whether a lease's owner is still alive. A TRANSIENT lease is owned by the
// long-lived `charly check run` orchestrator PROCESS; a PERSISTENT lease by the DEPLOYMENT (OR
// the creator's bring-up window).
func (a *ResourceArbiter) leaseLive(lz spec.PreemptLease) bool {
	if lz.Transient {
		return ownerAlive(lz.OwnerPID, lz.OwnerStart)
	}
	return a.running(lz.Claim) || ownerAlive(lz.OwnerPID, lz.OwnerStart)
}

// --- ledger I/O --------------------------------------------------------------------------------

func preemptLedgerPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "charly-preemption-leases.yml")
	}
	return filepath.Join(home, ".local", "share", "charly", "preemption", "leases.yml")
}

func (a *ResourceArbiter) loadLedger() (*spec.PreemptLedger, error) {
	data, err := os.ReadFile(a.ledgerPath)
	if os.IsNotExist(err) {
		return &spec.PreemptLedger{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading preemption ledger %s: %w", a.ledgerPath, err)
	}
	var l spec.PreemptLedger
	if err := yaml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parsing preemption ledger %s: %w", a.ledgerPath, err)
	}
	return &l, nil
}

func (a *ResourceArbiter) saveLedger(l *spec.PreemptLedger) error {
	if err := os.MkdirAll(filepath.Dir(a.ledgerPath), 0o755); err != nil {
		return fmt.Errorf("creating preemption dir: %w", err)
	}
	data, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	tmp := a.ledgerPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing preemption ledger: %w", err)
	}
	if err := os.Rename(tmp, a.ledgerPath); err != nil {
		return fmt.Errorf("committing preemption ledger: %w", err)
	}
	return nil
}

func (a *ResourceArbiter) acquireArbiterLock() (func() error, error) {
	return kit.AcquireFileLock(filepath.Join(filepath.Dir(a.ledgerPath), ".lock"), true)
}

func (a *ResourceArbiter) removeLease(claimant string) error {
	ledger, err := a.loadLedger()
	if err != nil {
		return err
	}
	out := ledger.Leases[:0]
	for _, lz := range ledger.Leases {
		if lz.Claimant != claimant {
			out = append(out, lz)
		}
	}
	ledger.Leases = out
	return a.saveLedger(ledger)
}
