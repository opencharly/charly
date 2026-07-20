package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// host_build_check_bed.go — the transitional "check-bed" host-session seam (P12 Wave-2,
// K5-mortal). command:check (candy/plugin-check) drives the R10 bed sequence
// (build → check box → deploy → check live → fresh update → tear down) over
// HostBuild("cli"), but runCheckBed opens a lock / lease / env lifecycle whose handles
// CANNOT cross the process boundary and MUST outlive many HostBuild("cli") calls:
//
//   - the bed flock + per-domain flocks are os.File fds the HOST holds through teardown;
//   - the preempt *Lease is a live core handle (the arbiter plugin);
//   - CHARLY_REPO_OVERRIDE / CHARLY_DEPLOY_CONFIG / CHARLY_PREEMPT_LEASE are set in the HOST
//     process so the HostBuild("cli")-forked `charly` children (hostBuildCli forks os.Args[0]
//     in this process) inherit them.
//
// So the host stands up a SESSION keyed by bed name — the exact pattern grpcSubstrateLifecycle
// uses (a package-level registry guarded by a mutex): `setup` inserts + acquires, `teardown`
// removes + releases, and the compiled-in plugin's HostBuild calls all land in the SAME host
// process so the live handles persist between ops. The `members-up`/`members-down`/`wait-ready`
// ops are the mid-sequence host-coupled helpers that run AFTER the substrate deploys (so cannot
// fold into setup) and call saveDeployState + libvirt + SSHExecutor/podman polls that have NO
// `charly` verb (so cannot be cli-reentry).
//
// The op bodies call the SHARED core helpers runCheckBed uses (bedGPUPrereqMissing,
// acquireFileLock, bedVmDomains/acquireVmDomainLock, selfSuperprojectOverridePair/
// mergeRepoOverrides, acquireResourceForClaimant, startLibvirtUserSession,
// persistBedDeployOverrides, bringUpMembers/tearDownMembers, waitForVmSshReady/
// waitForContainerReady, bedCheckLevel/bedCheckLiveRefs/…) — those helpers STAY core (shared
// with runCheckBed + bundle_add_cmd; K-wave relocation inventory, never aliased).
//
// TRANSITIONAL host seam — dies at K5: post-loaderkit the plugin holds its own flock via
// sdk/kit (filelock/install_ledger/deployconfig, the delivered K4-B state family), computes the
// repo-override itself, and calls the arbiter over InvokeProvider.
const checkBedBuilderKind = "check-bed"

// bedSession holds the live host handles a bed run owns across its HostBuild ops.
type bedSession struct {
	bed       string
	node      spec.BundleNode // resolved once at setup; drives the members/wait ops
	bedDomain string          // per-deploy VM domain identity (vmDomainIdentity(bed)); the live domain is charly-<bedDomain>
	imageTag  string          // per-RUN bed-scoped image tag (<bed>-<calver>); every box build + deploy in the run passes it as --tag (#75)
	bedUnlock func() error    // acquireFileLock(".check/<bed>/.lock")
	domUnlock []func() error  // acquireVmDomainLock per bedVmDomains, in acquire order
	lease     *Lease          // acquireResourceForClaimant

	// env restore state
	repoOvSet bool   // this session set CHARLY_REPO_OVERRIDE
	hadRepoOv bool   // it was already set (restore old) vs unset (Unsetenv)
	oldRepoOv string // the pre-existing value to restore
	cfgSet    bool   // this session set CHARLY_DEPLOY_CONFIG (owns the temp dir)
	cfgDir    string // MkdirTemp; teardown RemoveAll
}

var (
	bedSessMu   sync.Mutex
	bedSessions = map[string]*bedSession{} // keyed by bed name (mirror substrateLifecycle)
)

// release unwinds a session's acquired handles in REVERSE order (lease → env → domain
// locks → bed lock). ok controls the lease disposition: true → Lease.Release,
// false → Lease.ReleaseFailed (mirrors runCheckBed's deferred release). Nil-safe on every
// field so it doubles as the setup rollback (release whatever was acquired so far).
func (s *bedSession) release(ok bool) {
	if s.lease != nil {
		if ok {
			_ = s.lease.Release()
		} else {
			_ = s.lease.ReleaseFailed()
		}
		if s.lease.active {
			_ = os.Unsetenv(envPreemptLeaseHeld)
		}
	}
	if s.repoOvSet {
		if s.hadRepoOv {
			_ = os.Setenv(RepoOverrideEnv, s.oldRepoOv)
		} else {
			_ = os.Unsetenv(RepoOverrideEnv)
		}
	}
	if s.cfgSet {
		_ = os.Unsetenv(DeployConfigEnv)
		_ = os.RemoveAll(s.cfgDir)
	}
	for i := len(s.domUnlock) - 1; i >= 0; i-- {
		_ = s.domUnlock[i]()
	}
	if s.bedUnlock != nil {
		_ = s.bedUnlock()
	}
}

// lookupBedSession returns the live session for a bed (nil if absent).
func lookupBedSession(bed string) *bedSession {
	bedSessMu.Lock()
	defer bedSessMu.Unlock()
	return bedSessions[bed]
}

func hostBuildCheckBed(_ context.Context, req spec.CheckBedRequest, _ buildEngineContext) (spec.CheckBedReply, error) {
	switch req.Op {
	case "setup":
		return bedSessionSetup(req)
	case "members-up":
		s := lookupBedSession(req.Bed)
		if s == nil {
			return spec.CheckBedReply{}, fmt.Errorf("check-bed members-up: no live session for bed %q", req.Bed)
		}
		return spec.CheckBedReply{}, bringUpMembers(&s.node, s.imageTag)
	case "members-down":
		s := lookupBedSession(req.Bed)
		if s == nil {
			return spec.CheckBedReply{}, fmt.Errorf("check-bed members-down: no live session for bed %q", req.Bed)
		}
		return spec.CheckBedReply{}, tearDownMembers(&s.node)
	case "wait-ready":
		s := lookupBedSession(req.Bed)
		if s == nil {
			return spec.CheckBedReply{}, fmt.Errorf("check-bed wait-ready: no live session for bed %q", req.Bed)
		}
		if nodeTraits(&s.node).Venue == "ssh" { // vm (ssh venue)
			// Wait on the per-deploy DOMAIN IDENTITY (charly-<bedDomain> is the live domain +
			// managed ssh alias, post-P33), NOT the shared kind:vm entity (node.From).
			waitForVmSshReady(s.bedDomain)
		} else {
			waitForContainerReady(req.Bed)
		}
		return spec.CheckBedReply{}, nil
	case "teardown":
		return bedSessionTeardown(req)
	default:
		return spec.CheckBedReply{}, fmt.Errorf("check-bed: unknown op %q", req.Op)
	}
}

// bedSessionSetup opens the bed session — mirroring runCheckBed's acquire order
// (check_bed_run.go:361-484): GPU-prereq fail-fast, bed flock, per-domain flocks, repo-override
// env, deploy-config isolation, preempt lease, libvirt (vm/group), root deploy-override seed —
// then returns the BedDescriptor the kind-blind plugin drives the sequence from. Transactional:
// any acquire failure rolls back every handle taken so far (the session is NOT inserted, so the
// plugin never calls teardown for it).
func bedSessionSetup(req spec.CheckBedRequest) (spec.CheckBedReply, error) {
	dir := req.Dir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	// The bed must resolve against the parent superproject's in-development
	// candies on its very first load. Installing this after LoadUnified is too
	// late on a fresh cache: the pinned @github refs have already failed. Keep
	// the override active for the later cli children by transferring its restore
	// state into the bed session after the node is resolved.
	pair := selfSuperprojectOverridePair(dir)
	oldRepoOverride, hadRepoOverride := os.LookupEnv(RepoOverrideEnv)
	overrideSet := pair != ""
	overrideTransferred := false
	if overrideSet {
		_ = os.Setenv(RepoOverrideEnv, mergeRepoOverrides(oldRepoOverride, pair))
	}
	defer func() {
		if !overrideSet || overrideTransferred {
			return
		}
		if hadRepoOverride {
			_ = os.Setenv(RepoOverrideEnv, oldRepoOverride)
		} else {
			_ = os.Unsetenv(RepoOverrideEnv)
		}
	}()
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return spec.CheckBedReply{}, err
	}
	if !ok || uf == nil {
		return spec.CheckBedReply{}, fmt.Errorf("check-bed setup: no charly.yml in %s", dir)
	}
	node, isBed := uf.CheckBeds()[req.Bed]
	if !isBed {
		return spec.CheckBedReply{}, fmt.Errorf("check-bed setup: %q is not a disposable check bed", req.Bed)
	}

	// CalVer and logDir are single-sourced for both normal runs and prerequisite
	// skips. A normal run creates its directory only after it owns the per-bed
	// lock: a rejected duplicate must not masquerade as a newer incomplete run.
	calver := ComputeCalVer()
	logDir := filepath.Join(".check", req.Bed, calver)

	// Host-prerequisite fail-fast (BEFORE any acquire): a bed claiming a GPU resource whose vendor
	// has no matching card is unsatisfiable here — a clean SKIP (exit 3), not a failure. Acquires
	// NOTHING, so no session is inserted and no teardown is needed.
	if tok, vendor, missing := bedGPUPrereqMissing(node); missing {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return spec.CheckBedReply{}, fmt.Errorf("creating %s: %w", logDir, err)
		}
		return spec.CheckBedReply{
			Calver: calver,
			LogDir: logDir,
			PrereqSkip: &spec.CheckBedPrereqSkip{
				Token:  tok,
				Vendor: vendor,
				Reason: fmt.Sprintf("no GPU matching vendor %s on this host (bed requires resource %q)", vendor, tok),
			},
		}, nil
	}

	// The bed's per-deploy VM domain identity — charly-<bedDomain> is the live libvirt domain +
	// managed ssh alias (post-P33, keyed by the DEPLOY, not the shared kind:vm entity). Threaded
	// to the plugin in the reply so its `charly vm create/destroy/start` cli steps pass
	// --domain <bedDomain> (`vm build` stays entity-scoped); harmless (unused) for non-VM beds.
	s := &bedSession{bed: req.Bed, node: node, bedDomain: vmDomainIdentity(req.Bed), imageTag: bedRunImageTag(req.Bed, calver)}
	if overrideSet {
		s.repoOvSet = true
		s.hadRepoOv = hadRepoOverride
		s.oldRepoOv = oldRepoOverride
		overrideTransferred = true
		fmt.Fprintf(os.Stderr, "charly check run %s: testing LOCAL candies (%s += %s)\n", req.Bed, RepoOverrideEnv, pair)
	}
	inserted := false
	defer func() {
		if !inserted {
			s.release(true) // clean rollback — the bed never ran
		}
	}()

	// Per-bed exclusive lock — fail-fast on a duplicate concurrent run of the SAME bed.
	bedUnlock, lockErr := acquireFileLock(filepath.Join(".check", req.Bed, ".lock"), false)
	if lockErr != nil {
		if errors.Is(lockErr, errLockBusy) {
			return spec.CheckBedReply{}, fmt.Errorf("check bed %q is already running in this project — refusing a concurrent run (lock: .check/%s/.lock)", req.Bed, req.Bed)
		}
		return spec.CheckBedReply{}, fmt.Errorf("locking check bed %q: %w", req.Bed, lockErr)
	}
	s.bedUnlock = bedUnlock
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return spec.CheckBedReply{}, fmt.Errorf("creating %s: %w", logDir, err)
	}

	// Per-DOMAIN serialization for VM beds (sorted → no deadlock across a multi-domain bed).
	// Keyed by the DEPLOY (req.Bed) post-P33, so distinct beds sharing one kind:vm entity get
	// distinct, collision-free domains + run fully parallel.
	domains := bedVmDomains(req.Bed, node)
	for _, domain := range domains {
		du, derr := acquireVmDomainLock(domain)
		if derr != nil {
			return spec.CheckBedReply{}, fmt.Errorf("locking vm domain %s for bed %q: %w", domain, req.Bed, derr)
		}
		s.domUnlock = append(s.domUnlock, du)
	}

	// Isolate this bed's EPHEMERAL deploy state to a PER-BED config file so CONCURRENT beds never
	// share the operator's ~/.config/charly/charly.yml. Only set (and own cleanup) when not already set.
	if _, already := os.LookupEnv(DeployConfigEnv); !already {
		if cfgDir, mkErr := os.MkdirTemp("", "charly-bed-cfg-"+req.Bed+"-"); mkErr == nil {
			_ = os.Setenv(DeployConfigEnv, filepath.Join(cfgDir, "charly.yml"))
			s.cfgSet = true
			s.cfgDir = cfgDir
		}
	}

	// Resource arbitration (the preemptible axis): acquire a lease for the bed's requires_exclusive
	// / requires_shared claim (stopping any running preemptible holder; flipping a shared GPU). The
	// lease sets envPreemptLeaseHeld internally when active; teardown releases by req.OK.
	lease, lerr := acquireResourceForClaimant(req.Bed, node, true)
	if lerr != nil {
		return spec.CheckBedReply{}, fmt.Errorf("acquiring resources for %s: %w", req.Bed, lerr)
	}
	s.lease = lease

	isVM := nodeTraits(&node).Venue == "ssh"      // vm (ssh venue)
	isLocal := nodeTraits(&node).HostRooted       // local (host-rooted shell venue)
	isExternal := bedExternalInPlace(node.Target) // in-place external (bundle-del teardown)
	isGroup := node.IsGroup()

	// VM/group beds need the libvirt user-session daemon (probes + the backend resolver). Best-effort.
	if isVM || isGroup {
		startLibvirtUserSession()
	}

	// Seed the per-host overlay with the bed's project-declared deploy-shaped overrides BEFORE the
	// plugin's `charly config` step. persistBedDeployOverrides self-skips group + local + external
	// in-place; guarded to the non-VM path here so it matches runCheckBed EXACTLY (which seeds only
	// the pod/default root at :811 — a VM root's seed is pointless: a VM bed runs no `charly config`).
	if !isVM {
		persistBedDeployOverrides(req.Bed, node)
	}

	bedSessMu.Lock()
	bedSessions[req.Bed] = s
	bedSessMu.Unlock()
	inserted = true

	level := bedCheckLevel(uf, node)
	return spec.CheckBedReply{
		Calver:         calver,
		LogDir:         logDir,
		IsVM:           isVM,
		IsLocal:        isLocal,
		IsGroup:        isGroup,
		IsExternal:     isExternal,
		Image:          node.Image,  // "" for vm/local/group
		VMTemplate:     node.From,   // vm bed template (the ENTITY — `charly vm build` builds off this)
		BedDomain:      s.bedDomain, // per-deploy live domain (`charly vm create/destroy/start … --domain <this>`)
		ImageTag:       s.imageTag,
		LocalRef:       node.From, // local bed ref
		VMDomains:      domains,
		CheckLiveRefs:  deploykit.BedCheckLiveRefs(req.Bed, node.Children),
		ChildKeys:      deploykit.SortedNestedKeys(node.Children),
		LocalChildKeys: bedLocalChildKeys(node.Children),
		Members:        bedMemberDescriptors(node.Members),
		RunBuild:       kit.CheckLevelReaches(level, kit.CheckLevelBuild),
		RunRuntime:     kit.CheckLevelReaches(level, kit.CheckLevelNoAgent),
		RunAgent:       kit.CheckLevelReaches(level, kit.CheckLevelAgent),
	}, nil
}

// bedSessionTeardown closes the bed session — releasing every handle in reverse order and
// removing the session. Idempotent: a missing session (already torn down, or a GPU-skip that
// inserted none) is a no-op. req.OK controls the preempt-lease disposition.
func bedSessionTeardown(req spec.CheckBedRequest) (spec.CheckBedReply, error) {
	bedSessMu.Lock()
	s := bedSessions[req.Bed]
	delete(bedSessions, req.Bed)
	bedSessMu.Unlock()
	if s == nil {
		return spec.CheckBedReply{}, nil
	}
	s.release(req.OK)
	return spec.CheckBedReply{}, nil
}

// bedMemberDescriptors projects a group bed's sibling members into the wire descriptor the plugin
// drives its per-member image-build loop from (charly vm build <from> / box build <image> + check
// box, BEFORE the members-up op deploys them). Deterministic order (sortedMemberKeys). A vm member's
// From is the kind:vm ENTITY (build/spec source, entity-scoped — NOT --domain); the per-deploy member
// domain (vmDomainIdentity(memberKey)) is applied host-side by bringUpMembers, not here.
func bedMemberDescriptors(members map[string]*spec.BundleNode) []spec.CheckBedMember {
	keys := sortedMemberKeys(members)
	if len(keys) == 0 {
		return nil
	}
	out := make([]spec.CheckBedMember, 0, len(keys))
	for _, key := range keys {
		m := members[key]
		out = append(out, spec.CheckBedMember{Key: key, IsVM: isVmMember(m), Image: m.Image, From: m.From})
	}
	return out
}

// bedRunImageTag is the per-RUN bed-scoped image tag every `charly box build` +
// deploy step in a bed run passes as --tag: <bed-root-name>-<runCalver>. Keyed by
// the BED, not per-member — the collision unit is cross-BED (two beds building the
// same fixture image name from different trees racing the store-global
// short-name→newest-local-CalVer resolution); within one run the builds are already
// coordinated and different images sharing one tag string stay distinct name:tag
// pairs. The tag analogue of vmDomainIdentity (#33 domain=deploy-name), #75. Bed
// names are lowercase-hyphenated and calver is YYYY.DDD.HHMM — both valid OCI tag
// chars — so no sanitization is needed.
func bedRunImageTag(bed, calver string) string {
	if bed == "" || calver == "" {
		return ""
	}
	return bed + "-" + calver
}

// bedLocalChildKeys is the HOST-ROOTED (kind:local) subset of a node's nested children, in
// sortedNestedKeys order — the set a VM root deploys host-side (mirroring deployNestedLocalChildren:
// a VM's nested CONTAINER children are deployed in-guest by plugin-deploy-vm's PostApply, so a
// host-side re-deploy would be wrong).
func bedLocalChildKeys(children map[string]*spec.BundleNode) []string {
	var out []string
	for _, childKey := range deploykit.SortedNestedKeys(children) {
		child := children[childKey]
		if child != nil && nodeTraits(child).HostRooted { // local (host-rooted shell venue)
			out = append(out, childKey)
		}
	}
	return out
}

// Register the check-bed host-builder at package-var init (before any init(), like the
// cli / config-resolve / check-run builders).
var _ = func() bool {
	registerHostBuilder(checkBedBuilderKind, typedHostBuilder(checkBedBuilderKind, hostBuildCheckBed))
	return true
}()
