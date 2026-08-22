package check

// bed_session.go — the check-bed session, now fully plugin-side (#55 W3 B2-full, dissolving the
// former "check-bed" HostBuild seam entirely). Every piece of former core state this session
// managed turned out to be reachable without a host round-trip:
//
//   - the bed flock + per-domain flocks: spec/lock is plugin-importable fabric (not sdk-mechanism
//     private) — a compiled-in plugin can hold the returned unlock funcs itself, same as core did.
//   - the preempt lease: candy/plugin-vm/vm_arbiter_shim.go already proves a plugin reaches
//     verb:arbiter directly via InvokeProvider, bypassing core's former arbiterProxy entirely —
//     mirrored here (arbiterAcquire/arbiterRelease).
//   - the repo-override / deploy-config env vars: this plugin is COMPILED-IN, so os.Setenv here
//     lands in the SAME process env hostBuildCli's cli-reentry children fork from — the ONE
//     genuine placement constraint this session carries (see the header note below).
//
// No package-level session MAP is needed (unlike the former core session, which had to survive
// separate HostBuild round-trips across a process-reentry boundary): the whole bed run is now ONE
// in-process Go call graph — runCheckBed (bed_run.go) holds the *bedSession its own bedSetup call
// returns in a local variable for the run's whole lifetime, threading it explicitly into
// bedTeardown at the end. Members-up/-down call sdk/deploykit.BringUpMembers/TearDownMembers
// directly (#55 W3 A4) using data already in the caller's *spec.CheckBedReply — no session lookup.
//
// PLACEMENT CLASS: compiled-in-REQUIRED. This is not incidental to today's placement — it is a
// structural requirement, the SAME documented class as a bootstrap plugin. hostBuildCli (the
// generic "cli" HostBuild seam every `charly <verb>` cli-reentry step rides) ALWAYS forks its
// child in the CORE process; spec.CliRequest carries no per-call Env field to thread the 3
// process-global vars (CHARLY_REPO_OVERRIDE/CHARLY_DEPLOY_CONFIG/CHARLY_PREEMPT_LEASE) explicitly
// instead. If this plugin were EVER placed out-of-process, its os.Setenv calls would land in the
// WRONG process and every cli-reentry step in a bed run would silently lose the override/isolation
// — this is why the check-bed session specifically (not command:check as a whole) requires
// compiled-in placement. The de-coupling path is REGISTERED, not implemented: extending
// spec.CliRequest with an optional Env map (a field-extension of an EXISTING wire input, the SAME
// class of change A2 used for the arbiter's implied-GPU field) would let a future out-of-process
// bed runner thread these vars per-call instead of relying on shared process env — explicit data
// over ambient env is the better end-state, but implementing it is not required for THIS
// dissolution and is deferred as a named IOU.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/fleet"
	"github.com/opencharly/spec/hostenv"
	"github.com/opencharly/spec/lock"
	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"
)

// envPreemptLeaseHeld is set by the OUTERMOST claim-bringing `charly` invocation (a check-bed run,
// or a standalone `charly vm create`/`charly start`) so the nested `charly` subprocesses it spawns
// do NOT independently acquire/release the lease — the owner manages it. Mirrors the
// identically-named, identically-valued const that lived in charly/preempt.go (deleted, K-wave 2
// cone CONTESTED — the surviving core copy is the op="remove" release chain in
// host_build_pod_lifecycle_dispatch.go; both sides of the SAME process-env contract; not an alias
// — a plugin cannot import charly package main).
const envPreemptLeaseHeld = "CHARLY_PREEMPT_LEASE"

// bedSession holds the live host handles ONE bed run owns across its lifecycle — from bedSetup's
// return to bedTeardown's call, all within the SAME in-process Go call graph.
type bedSession struct {
	bedUnlock func() error
	domUnlock []func() error

	leaseClaimant string // "" ⇒ acquireLease never ran (a GPU-prereq skip returns before it)
	leaseActive   bool

	repoOvSet bool   // this session set CHARLY_REPO_OVERRIDE
	hadRepoOv bool   // it was already set (restore old) vs unset (Unsetenv)
	oldRepoOv string // the pre-existing value to restore

	cfgSet bool   // this session set CHARLY_DEPLOY_CONFIG (owns the temp dir)
	cfgDir string // MkdirTemp; teardown RemoveAll
}

// release unwinds a session's acquired handles in REVERSE order (lease → env → domain locks →
// bed lock). ok controls the lease disposition. Nil-safe on every field so it doubles as the
// bedSetup rollback (release whatever was acquired so far on a later-step failure).
func (s *bedSession) release(ctx context.Context, ex *sdk.Executor, ok bool) {
	if s == nil {
		return
	}
	if s.leaseClaimant != "" && s.leaseActive {
		// Mirrors the deleted charly/preempt.go's Lease.Release/ReleaseFailed early-out on
		// !active — a lease this session never actually claimed (because an outer orchestrator
		// already held it, or the claimant declared no requires_exclusive/requires_shared) needs
		// no release call.
		_ = arbiterRelease(ctx, ex, s.leaseClaimant, ok)
		_ = os.Unsetenv(envPreemptLeaseHeld)
	}
	if s.repoOvSet {
		if s.hadRepoOv {
			_ = os.Setenv(proc.RepoOverrideEnv, s.oldRepoOv)
		} else {
			_ = os.Unsetenv(proc.RepoOverrideEnv)
		}
	}
	if s.cfgSet {
		// An ephemeral registration is TWO artifacts — the state in this overlay and an armed
		// systemd TTL timer — and this teardown used to destroy only the first. The timer then
		// fired later against an overlay that no longer held the entity: it could neither resolve
		// it nor verify its identity, so the VM it was registered to reap leaked permanently and
		// the unit accumulated as a failure nobody reads. Measured 2026-08-15: ten armed units for
		// one reused entity name, all firing at once, none able to reap.
		//
		// Cancel BEFORE removing the state, so a cancellation failure leaves the overlay intact and
		// the timer still able to work — the safe ordering of two operations that must both happen.
		cancelBedEphemeralTimers(ctx, ex)
		_ = os.Unsetenv(spec.DeployConfigEnv)
		_ = os.RemoveAll(s.cfgDir)
	}
	for i := len(s.domUnlock) - 1; i >= 0; i-- {
		_ = s.domUnlock[i]()
	}
	if s.bedUnlock != nil {
		_ = s.bedUnlock()
	}
}

// arbiterInvoke resolves verb:arbiter and Invokes it with an action-tagged input — the SAME
// direct-InvokeProvider(verb,"arbiter") pattern candy/plugin-vm/vm_arbiter_shim.go already proves
// bypasses core's former arbiterProxy entirely.
func arbiterInvoke(ctx context.Context, ex *sdk.Executor, in spec.ArbiterInvokeInput) (spec.ArbiterInvokeReply, error) {
	params, err := json.Marshal(in)
	if err != nil {
		return spec.ArbiterInvokeReply{}, err
	}
	out, err := ex.InvokeProvider(ctx, "verb", "arbiter", sdk.OpRun, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return spec.ArbiterInvokeReply{}, fmt.Errorf("arbiter %s: %w", in.Action, err)
	}
	var reply spec.ArbiterInvokeReply
	if len(out) > 0 {
		if uerr := json.Unmarshal(out, &reply); uerr != nil {
			return spec.ArbiterInvokeReply{}, uerr
		}
	}
	if reply.Error != "" {
		return spec.ArbiterInvokeReply{}, errors.New(reply.Error)
	}
	return reply, nil
}

// arbiterAcquire acquires the appropriate lease for a claimant: EXCLUSIVE when it declares
// requires_exclusive, otherwise SHARED (a no-op inside the arbiter when the claimant declares no
// requires_shared AND implies no GPU consumption — the arbiter auto-promotes an implied GPU
// consumer, K-wave W3a A2). Skips entirely when an outer orchestrator already owns the lease
// (envPreemptLeaseHeld) — mirrors the deleted charly/preempt.go's acquireResourceForClaimant/
// acquireExclusiveForClaimant/acquireSharedForClaimant/acquireDispatch chain exactly (that chain
// was DEAD in core — zero production callers — and DELETED at K-wave 2 cone CONTESTED; its
// per-caller copies are what survive), restore guarantees included: the arbiter's
// crash-safety/lease-ledger/poison-marker/liveness state lives ENTIRELY plugin-side in
// candy/plugin-preempt already, keyed by Claimant — so `charly preempt restore` reconciles
// identically regardless of which caller acquired the lease, AS LONG AS the ArbiterInvokeInput
// shape matches the former core acquireDispatch field-for-field (verified below:
// Action/Claimant/Tokens/ClaimAddr/Transient/IsGroup/IsPodMember/SecurityDevices, the same 8
// fields in the same shapes).
func arbiterAcquire(ctx context.Context, ex *sdk.Executor, claimant string, node spec.FleetNode, transient bool) (active bool, err error) {
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return false, nil
	}
	action := spec.ArbiterActionAcquireShared
	tokens := spec.DedupeNonEmpty(node.RequiredShared())
	if len(node.RequiredExclusive()) > 0 {
		action = spec.ArbiterActionAcquireExclusive
		tokens = spec.DedupeNonEmpty(node.RequiredExclusive())
	}
	var secDevices []string
	if node.Security != nil {
		secDevices = node.Security.Devices
	}
	r, ierr := arbiterInvoke(ctx, ex, spec.ArbiterInvokeInput{
		Action:          action,
		Claimant:        claimant,
		Tokens:          tokens,
		ClaimAddr:       fleet.HolderAddrFor(claimant, node),
		Transient:       transient,
		IsGroup:         node.IsGroup(),
		IsPodMember:     fleet.IsContainerVenue(&node),
		SecurityDevices: secDevices,
	})
	if ierr != nil {
		return false, ierr
	}
	if r.Active {
		_ = os.Setenv(envPreemptLeaseHeld, claimant)
	}
	return r.Active, nil
}

// arbiterRelease restores the holders a claimant's lease stopped + removes the lease. ok controls
// the disposition: true → the SAME "Release" (claim succeeded) semantics core's Lease.Release
// applied, false → "ReleaseFailed" (on-success holders stay stopped). The caller (bedSession.
// release) only invokes this when leaseActive is true — a lease this session never actually
// claimed (an outer orchestrator already held it, or the claimant declared neither
// requires_exclusive nor requires_shared) needs no release call, mirroring core's
// Lease.Release/ReleaseFailed early-out on !active.
func arbiterRelease(ctx context.Context, ex *sdk.Executor, claimant string, ok bool) error {
	r, err := arbiterInvoke(ctx, ex, spec.ArbiterInvokeInput{Action: spec.ArbiterActionRelease, Claimant: claimant, Success: ok})
	if err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// bedGpuPrereqCheck reaches the ONE narrow HostBuild seam surviving check-bed's dissolution — GPU
// host-DETECTION is the project's explicitly operator-dropped exception (see
// charly/host_build_check_bed_gpu_prereq.go's header). tokens is the claimant's RAW
// (RequiredExclusive ++ RequiredShared) list — the fenced core function re-dedupes internally.
func bedGpuPrereqCheck(ctx context.Context, ex *sdk.Executor, tokens []string) (missing bool, token, vendor string, err error) {
	reqJSON, err := json.Marshal(spec.CheckBedGpuPrereqRequest{Tokens: tokens})
	if err != nil {
		return false, "", "", err
	}
	out, err := ex.HostBuild(ctx, "check-bed-gpu-prereq", reqJSON)
	if err != nil {
		return false, "", "", err
	}
	var reply spec.CheckBedGpuPrereqReply
	if uerr := json.Unmarshal(out, &reply); uerr != nil {
		return false, "", "", uerr
	}
	return reply.Missing, reply.Token, reply.Vendor, nil
}

// bedCheckLevel resolves the acceptance-depth rung for a bed from its box's authored check_level
// (none → DefaultCheckLevel). VM/local beds carry no box image, so they always run at the default
// rung. Ported from charly/check_bed_run.go — uf.ProjectConfig() is a plain spec.UnifiedFile
// method, no core-only coupling.
func bedCheckLevel(uf *spec.UnifiedFile, node spec.FleetNode) string {
	if node.Image == "" {
		return spec.DefaultCheckLevel
	}
	bc, _, ok := uf.ProjectConfig().ResolveBoxRef(node.Image)
	if !ok {
		return spec.DefaultCheckLevel
	}
	return spec.ResolveCheckLevel(bc.CheckLevel)
}

// bedMemberDescriptors projects a group bed's sibling members into the descriptor the plugin
// drives its per-member image-build loop from. Ported from charly/host_build_check_bed.go, using
// fleet.IsVmVenue instead of the former core-private isVmMember (same Descent-stamped read).
func bedMemberDescriptors(members map[string]*spec.FleetNode) []spec.CheckBedMember {
	keys := spec.SortedMemberKeys(members)
	if len(keys) == 0 {
		return nil
	}
	out := make([]spec.CheckBedMember, 0, len(keys))
	for _, key := range keys {
		m := members[key]
		out = append(out, spec.CheckBedMember{Key: key, IsVM: fleet.IsVmVenue(m), Image: m.Image, From: m.From})
	}
	return out
}

// bedRunImageTag is the per-RUN bed-scoped image tag every `charly box build` + deploy step in a
// bed run passes as --tag: <bed-root-name>-<runCalver> (#75). Ported unchanged from
// charly/host_build_check_bed.go — pure string concat, no core coupling.
func bedRunImageTag(bed, calver string) string {
	if bed == "" || calver == "" {
		return ""
	}
	return bed + "-" + calver
}

// bedLocalChildKeys is the HOST-ROOTED (kind:local) subset of a node's nested children, in
// sortedNestedKeys order — the set a VM root deploys host-side. Ported from
// charly/host_build_check_bed.go, using fleet.HostRooted instead of the former core-private
// nodeTraits(child).HostRooted read (same Descent-stamped predicate, already promoted #55 U4).
func bedLocalChildKeys(children map[string]*spec.FleetNode) []string {
	var out []string
	for _, childKey := range fleet.SortedNestedKeys(children) {
		child := children[childKey]
		if fleet.HostRooted(child) {
			out = append(out, childKey)
		}
	}
	return out
}

// bedSetup opens the bed session — mirroring the former host_build_check_bed.go's
// bedSessionSetup acquire order (GPU-prereq fail-fast, bed flock, per-domain flocks,
// repo-override env, deploy-config isolation, preempt lease, libvirt) — then returns the
// BedDescriptor runCheckBed drives the sequence from. Transactional: any acquire failure rolls
// back every handle taken so far via the returned session's release (the caller must call it on
// any non-nil-session error path too — see bed_run.go's runCheckBed).
func bedSetup(ctx context.Context, ex *sdk.Executor, bed, dir string) (spec.CheckBedReply, *bedSession, error) {
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	// The bed must resolve against the parent superproject's in-development candies on its very
	// first load (matching the former host's own comment verbatim — the override must be live
	// BEFORE the self-load below, on a fresh cache the pinned @github refs have already failed by
	// the time a later override would apply).
	pair := proc.SelfSuperprojectOverridePair(dir)
	oldRepoOverride, hadRepoOverride := os.LookupEnv(proc.RepoOverrideEnv)
	overrideSet := pair != ""
	overrideTransferred := false
	if overrideSet {
		_ = os.Setenv(proc.RepoOverrideEnv, proc.MergeRepoOverrides(oldRepoOverride, pair))
	}
	defer func() {
		if !overrideSet || overrideTransferred {
			return
		}
		if hadRepoOverride {
			_ = os.Setenv(proc.RepoOverrideEnv, oldRepoOverride)
		} else {
			_ = os.Unsetenv(proc.RepoOverrideEnv)
		}
	}()

	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir)
	if err != nil {
		return spec.CheckBedReply{}, nil, err
	}
	if !ok || uf == nil {
		return spec.CheckBedReply{}, nil, fmt.Errorf("check-bed setup: no charly.yml in %s", dir)
	}
	node, isBed := uf.CheckBeds()[bed]
	if !isBed {
		return spec.CheckBedReply{}, nil, fmt.Errorf("check-bed setup: %q is not a disposable check bed", bed)
	}

	// CalVer and logDir are single-sourced for both normal runs and prerequisite skips.
	calver := spec.ComputeCalVer()
	logDir := filepath.Join(".check", bed, calver)

	// Host-prerequisite fail-fast (BEFORE any acquire): a clean SKIP (exit 3), not a failure.
	// Acquires NOTHING, so no session handles exist and no teardown is needed.
	tokens := append(append([]string{}, node.RequiredExclusive()...), node.RequiredShared()...)
	if missing, tok, vendor, gerr := bedGpuPrereqCheck(ctx, ex, tokens); gerr == nil && missing {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return spec.CheckBedReply{}, nil, fmt.Errorf("creating %s: %w", logDir, err)
		}
		return spec.CheckBedReply{
			Calver: calver,
			LogDir: logDir,
			PrereqSkip: &spec.CheckBedPrereqSkip{
				Token:  tok,
				Vendor: vendor,
				Reason: fmt.Sprintf("no GPU matching vendor %s on this host (bed requires resource %q)", vendor, tok),
			},
		}, nil, nil
	}

	bedDomain := spec.VmDomainIdentity(bed)
	imageTag := bedRunImageTag(bed, calver)
	s := &bedSession{}
	if overrideSet {
		s.repoOvSet = true
		s.hadRepoOv = hadRepoOverride
		s.oldRepoOv = oldRepoOverride
		overrideTransferred = true
		fmt.Fprintf(os.Stderr, "charly check run %s: testing LOCAL candies (%s += %s)\n", bed, proc.RepoOverrideEnv, pair)
	}
	rolledBack := false
	rollback := func() {
		if !rolledBack {
			rolledBack = true
			s.release(ctx, ex, true) // clean rollback — the bed never ran
		}
	}

	// Per-bed exclusive lock — fail-fast on a duplicate concurrent run of the SAME bed.
	bedUnlock, lockErr := lock.AcquireFileLock(filepath.Join(".check", bed, ".lock"), false)
	if lockErr != nil {
		rollback()
		if errors.Is(lockErr, lock.ErrLockBusy) {
			return spec.CheckBedReply{}, nil, fmt.Errorf("check bed %q is already running in this project — refusing a concurrent run (lock: .check/%s/.lock)", bed, bed)
		}
		return spec.CheckBedReply{}, nil, fmt.Errorf("locking check bed %q: %w", bed, lockErr)
	}
	s.bedUnlock = bedUnlock
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		rollback()
		return spec.CheckBedReply{}, nil, fmt.Errorf("creating %s: %w", logDir, err)
	}

	// Per-DOMAIN serialization for VM beds (sorted → no deadlock across a multi-domain bed).
	domains := lock.BedVmDomains(bed, node)
	for _, domain := range domains {
		du, derr := lock.AcquireVmDomainLock(domain)
		if derr != nil {
			rollback()
			return spec.CheckBedReply{}, nil, fmt.Errorf("locking vm domain %s for bed %q: %w", domain, bed, derr)
		}
		s.domUnlock = append(s.domUnlock, du)
	}

	// Isolate this bed's EPHEMERAL deploy state to a PER-BED config file so CONCURRENT beds never
	// share the operator's ~/.config/charly/charly.yml. Only set (and own cleanup) when not already set.
	if _, already := os.LookupEnv(spec.DeployConfigEnv); !already {
		if cfgDir, mkErr := os.MkdirTemp("", "charly-bed-cfg-"+bed+"-"); mkErr == nil {
			_ = os.Setenv(spec.DeployConfigEnv, filepath.Join(cfgDir, "charly.yml"))
			s.cfgSet = true
			s.cfgDir = cfgDir
		}
	}

	// Resource arbitration (the preemptible axis): acquire a lease for the bed's requires_exclusive
	// / requires_shared claim.
	active, lerr := arbiterAcquire(ctx, ex, bed, node, true)
	if lerr != nil {
		rollback()
		return spec.CheckBedReply{}, nil, fmt.Errorf("acquiring resources for %s: %w", bed, lerr)
	}
	s.leaseClaimant = bed
	s.leaseActive = active

	isVM := fleet.IsVmVenue(&node)
	isLocal := fleet.HostRooted(&node)
	isExternal := fleet.ExternalInPlaceVenue(&node)
	isGroup := node.IsGroup()

	// VM/group beds need the libvirt user-session daemon (probes + the backend resolver). Best-effort.
	if isVM || isGroup {
		hostenv.StartLibvirtUserSession()
	}

	level := bedCheckLevel(uf, node)
	nodeJSON, _ := json.Marshal(node)
	return spec.CheckBedReply{
		Calver:         calver,
		LogDir:         logDir,
		IsVM:           isVM,
		IsLocal:        isLocal,
		IsGroup:        isGroup,
		IsExternal:     isExternal,
		NodeJSON:       nodeJSON,
		Image:          node.Image,
		HasAddCandy:    len(node.AddCandy) > 0,
		VMTemplate:     node.From,
		BedDomain:      bedDomain,
		ImageTag:       imageTag,
		LocalRef:       node.From,
		VMDomains:      domains,
		CheckLiveRefs:  fleet.BedCheckLiveRefs(bed, node.Children),
		ChildKeys:      fleet.SortedNestedKeys(node.Children),
		LocalChildKeys: bedLocalChildKeys(node.Children),
		Members:        bedMemberDescriptors(node.Members),
		RunBuild:       spec.CheckLevelReaches(level, spec.CheckLevelBuild),
		RunRuntime:     spec.CheckLevelReaches(level, spec.CheckLevelNoAgent),
		RunAgent:       spec.CheckLevelReaches(level, spec.CheckLevelAgent),
	}, s, nil
}

// bedTeardown closes the bed session — releasing every handle in reverse order. Idempotent-safe
// via bedSession.release's nil-checks; ok controls the preempt-lease disposition.
func bedTeardown(ctx context.Context, ex *sdk.Executor, sess *bedSession, ok bool) {
	sess.release(ctx, ex, ok)
}
