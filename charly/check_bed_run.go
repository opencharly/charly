package main

// check_bed_run.go — the HOST-side bed helpers the check-bed session seam
// (host_build_check_bed.go) shares with the deploy path (bundle_members.go).
//
// The `charly check run <bed>` R10 acceptance sequence (build → check box → deploy →
// check live → fresh update → tear down) lives in the compiled-in command:check plugin
// (candy/plugin-check); it drives the sequence over HostBuild("cli") + the check-bed
// session seam. This file keeps only the loader/host Mechanisms a plugin (a separate
// module importing only sdk) cannot perform and which the seam's session ops call:
// the per-domain flock helpers
// (bedVmDomains / acquireVmDomainLock), the acceptance-depth resolver (bedCheckLevel), the
// in-place-external classifier (bedExternalInPlace), the per-host deploy-override seed
// (persistBedDeployOverrides), the nested-local-children apply (deployNestedLocalChildren),
// and the readiness gates (waitForVmSshReady / waitForContainerReady).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/deploykit"
)

// bedVmDomains returns the sorted, deduped libvirt domain names (charly-<from>) a bed's
// VM(s) occupy — the bed's own vm target plus any group-member vm targets. This is the
// unit of exclusive host contention two DISTINCT beds can collide on (the per-domain lock
// in the check-bed session seam serializes them).
func bedVmDomains(name string, node spec.BundleNode) []string {
	seen := map[string]bool{}
	var out []string
	add := func(domainID string) {
		if domainID == "" {
			return
		}
		dom := "charly-" + domainID
		if seen[dom] {
			return
		}
		seen[dom] = true
		out = append(out, dom)
	}
	// Post-P33 the domain is keyed by the DEPLOY (charly-<domainIdentity>), not the shared entity:
	// the VM root's domain derives from the bed name, each VM member's from its member key. So
	// distinct beds sharing one kind:vm entity now hold DISTINCT domain locks and run fully parallel
	// (the collision-free-by-construction goal); the lock only serializes two invocations of the SAME
	// deploy on its own domain.
	if nodeTraits(&node).Venue == "ssh" { // vm (ssh venue) root
		add(vmDomainIdentity(name))
	}
	for memberKey, m := range node.Members {
		if isVmMember(m) {
			add(vmDomainIdentity(memberKey))
		}
	}
	sort.Strings(out)
	return out
}

// acquireVmDomainLock takes a BLOCKING, host-global advisory lock serializing every check
// bed that occupies the given libvirt domain. Host-global (under ~/.cache/charly/.locks/)
// because the qemu:///session domain namespace is host-wide, shared across project dirs.
func acquireVmDomainLock(domain string) (func() error, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".cache", "charly", ".locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return acquireFileLock(filepath.Join(dir, "vm-domain-"+domain+".lock"), true)
}

// bedCheckLevel resolves the acceptance-depth rung for a bed from its box's
// authored check_level (none → DefaultCheckLevel). VM / local beds carry no box
// image, so they always run at the default rung.
func bedCheckLevel(uf *UnifiedFile, node spec.BundleNode) string {
	if node.Image == "" {
		return kit.DefaultCheckLevel
	}
	if bc, _, ok := uf.ProjectConfig().resolveBoxRef(node.Image); ok {
		return kit.ResolveCheckLevel(bc.CheckLevel)
	}
	return kit.DefaultCheckLevel
}

// bedExternalInPlace reports whether a bed ROOT's substrate is an EXTERNAL deploy substrate
// that applies its workload IN PLACE — local-like: NO container image to build, NO `charly
// config`/`charly start`, teardown via `charly bundle del` (replay the recorded reverse
// ops). local/android/k8s/exampledeploy are in-place (they carry no `image:`).
//
// pod is the ONE externalized substrate that is NOT in-place: it builds + runs a container
// image and keeps the FULL pod lifecycle (image build → config → start → check-live →
// `charly remove`), so the bed runner must drive it through the DEFAULT pod
// path exactly as the in-proc pod — only the `charly bundle add` overlay build internally
// routes through pod's external deploy target + lifecycle hook now (invisible to the bed
// runner). Excluding pod here is consistent with the bed runner's other substrate-identity
// checks (isVM = ssh venue, isLocal = host-rooted venue); vm sidesteps the in-place logic
// via its own `case isVM` branch, so this exclusion is the container-venue (pod) analogue.
// P9: exclude the CONTAINER venue by the stamped trait, not the substrate kind word.
func bedExternalInPlace(target string) bool {
	return isExternalDeploySubstrate(target) && deployTraitDescent(target).Venue != "container"
}

// persistBedDeployOverrides seeds the per-host charly.yml with a kind:check
// bed's project-declared deploy-shaped fields (port / volume / env / tunnel /
// security / network), its disposable/lifecycle classification, AND its
// resource-arbitration role (preemptible holder / requires_exclusive /
// requires_shared claimant), BEFORE the bed's `charly config` step runs. Seeding
// the arbiter fields is what lets a bed/deploy MEMBER be an arbiter participant:
// bringUpMembers persists each member here, then the member's `charly start`
// reloads the per-host node and fires the arbiter off these fields (start.go →
// acquireResourceForClaimant; preempt.go's holder gather) — without them a
// member's requires_exclusive reloaded as [] and the arbiter silently no-op'd.
// The folded bed node is the source of truth, but
// `charly bundle add` / `charly config` otherwise source those fields from the IMAGE
// LABELS and gate port writes behind an operator `-p` — so a bed's declared
// `port:` remap would never reach the quadlet (it would fall back to the image
// default and collide with any same-image deploy already bound to that port).
// Seeding the per-host entry up front lets the existing
// MergeDeployOntoMetadata → quadlet path honor the overrides with no new merge
// logic; `charly config`'s own SetPorts-gated save then leaves the seeded port
// untouched (it passes no `-p`). saveDeployState's per-field guards make
// unset bed fields no-ops, so this is safe for beds that declare only a subset.
func persistBedDeployOverrides(name string, node spec.BundleNode) {
	// A GROUP bed (boxless root + sibling Members — the §3 cross-deployment
	// shape) has NO root deployment to seed: its members each carry their own
	// port/volume/env overrides (bringUpMembers persists every member), and the
	// boxless root is never `charly config`'d. Persisting the group root here would
	// write a MEMBERLESS bed (no box, no members — saveDeployState carries no
	// member fields) that validateCheckBeds then HARD-REJECTS on the next overlay
	// load ("no workload cross-ref and no sibling members"), poisoning every
	// subsequent saveDeployState. So never persist a group bed root.
	if node.IsGroup() {
		return
	}
	// A LOCAL or EXTERNAL in-place bed never runs `charly config` (it applies candies
	// in place during `charly bundle add`), so the whole reason persistBedDeployOverrides
	// exists — seeding
	// port/volume/env overrides BEFORE config — does not apply. Worse, a local bed's
	// only persistable
	// cross-ref is its `local:` template, which lives in the bed's OWN project; writing
	// it into the GLOBAL per-host overlay makes that overlay un-loadable from every
	// OTHER project (validateCheckBeds: "references local template … which is not
	// defined"), poisoning concurrent/cross-project bed runs. Local deploys persist via
	// the install ledger, not this bundle-map path, so skipping is also lossless.
	if nodeTraits(&node).HostRooted || bedExternalInPlace(node.Target) { // local (host-rooted) or in-place external
		return
	}
	deploykit.SaveDeployState(name, "", deploykit.SaveDeployStateInput{
		Ports:         node.Port,
		SetPorts:      len(node.Port) > 0,
		Volume:        node.Volume,
		Env:           node.Env,
		CleanEnv:      true,
		Tunnel:        node.Tunnel,
		Security:      node.Security,
		Network:       node.Network,
		Box:           node.Image,
		Target:        node.Target,
		SetDisposable: true,
		Disposable:    node.IsDisposable(),
		SetLifecycle:  node.Lifecycle != "",
		Lifecycle:     node.Lifecycle,
		// Resource-arbitration role — so a group MEMBER (holder / claimant) can
		// actually drive the arbiter after its `charly start` reloads this entry.
		Preemptible:       node.Preemptible,
		RequiresExclusive: node.RequiresExclusive,
		RequiresShared:    node.RequiresShared,
	}, marshalDeployNode)
}

// deployNestedLocalChildren deploys a VM's nested target:local children via the
// dotted-path dispatch, which applies each child's local-deploy candies INSIDE the
// guest over the NestedExecutor (SSH).
//
// plugin-deploy-vm's PostApply brings up nested target:pod children as in-guest
// quadlets, but it SKIPS target:local children — they carry no image, they apply
// candies in place. Without this loop a nested local child never deploys, and a
// deploy-scope check against it either fails or (worse) silently checks nothing.
//
// Both sites that own a VM venue call this: the isVM bed ROOT and bringUpMembers'
// VM-member branch. They differ only in how a child deploy is executed (the root
// wraps it in a recorded step(); a member shells out directly), so that is the
// injected apply func.
func deployNestedLocalChildren(parent string, children map[string]*spec.BundleNode, apply func(childKey, dotted string) error) error {
	for _, childKey := range deploykit.SortedNestedKeys(children) {
		child := children[childKey]
		if child == nil || !nodeTraits(child).HostRooted { // local (host-rooted shell venue) only
			continue // container/vm children handled in-guest by plugin-deploy-vm's PostApply
		}
		if err := apply(childKey, parent+"."+childKey); err != nil {
			return fmt.Errorf("deploy nested local child %s.%s: %w", parent, childKey, err)
		}
	}
	return nil
}

// waitForVmSshReady gates on the VM being SSH-reachable AND cloud-init having
// settled, using the SAME deterministic SSHExecutor preflight the VM check-live
// path (check_cmd.go) and the external vm deploy walk run — NOT a fixed sleep. WaitForSSH
// polls until sshd answers; WaitForCloudInit retries until an ssh connection
// survives a `cloud-init status` poll (the deterministic cloud-init-settled
// signal — so deploy-add never races a still-running first-boot pacman). vmName
// is the kind:vm entity name. Best-effort: silent on timeout — the downstream
// deploy-add surfaces the real error.
// waitForVmSshReady polls the managed alias charly-<domainID> until the guest's sshd answers and
// cloud-init settles. domainID is the per-deploy DOMAIN IDENTITY (the bed/member deploy name), not
// the shared kind:vm entity — the alias the create path published.
func waitForVmSshReady(domainID string) {
	gate := &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 5}
	ctx := context.Background()
	if err := gate.WaitForSSH(ctx); err != nil {
		return
	}
	_ = gate.WaitForCloudInit(ctx)
}

// waitForContainerReady gates on the container being exec-able AND its
// supervisord-managed children having left their transitional states, so a
// one-shot check-live port/service probe never races a child that has not yet
// bound. `charly start` returns when systemd reports the service active, but
// supervisord's autostart children are still STARTING for a moment after. This
// polls `supervisorctl status` until no child is STARTING/BACKOFF (a child binds
// its port the instant it reaches RUNNING) instead of sleeping a fixed,
// host-tuned interval. Images without supervisord settle immediately. Best-effort:
// silent on timeout (the next check-live step surfaces the real failure).
func waitForContainerReady(bed string) {
	containerName := "charly-" + bed
	// supervisorStatus reports __NOSUP__ when the image has no supervisorctl, so
	// "no supervisord" is distinguishable from "socket not up yet".
	const supervisorStatus = `command -v supervisorctl >/dev/null 2>&1 || { echo __NOSUP__; exit 0; }; supervisorctl status 2>&1`
	// MONOTONIC readiness via the unified pollUntil primitive (poll.go): the
	// progress marker is the count of SETTLED children — it climbs as children
	// reach RUNNING, so a slow startup under heavy parallel load is waited for
	// (the no-progress watchdog resets on each new settled child); a child
	// crash-looping back to BACKOFF drops the count below its high-water, so the
	// watchdog correctly does NOT treat the flap as progress and the bed stalls
	// out instead of hiding the fault. Replaces the fixed 30s deadline (the most
	// load-fragile in the old set). Best-effort: silent on stall/cap (the next
	// check-live step surfaces the real failure).
	cfg := loadedReadiness().Wait("container-ready "+bed, PollLocal)
	_ = pollUntil(context.Background(), cfg, func(actx context.Context) (bool, float64, error) {
		if exec.CommandContext(actx, "podman", "exec", containerName, "true").Run() != nil {
			return false, 0, nil // container not exec-able yet
		}
		out, _ := exec.CommandContext(actx, "podman", "exec", containerName, "sh", "-c", supervisorStatus).CombinedOutput()
		if bytes.Contains(out, []byte("__NOSUP__")) {
			return true, 0, nil // no supervisord — nothing to settle
		}
		settled := float64(bytes.Count(out, []byte("RUNNING")) + bytes.Count(out, []byte("STOPPED")) +
			bytes.Count(out, []byte("EXITED")) + bytes.Count(out, []byte("FATAL")))
		if bytes.Contains(out, []byte("STARTING")) || bytes.Contains(out, []byte("BACKOFF")) {
			return false, settled, nil // children still coming up
		}
		if settled > 0 {
			return true, settled, nil // supervisord answered + nothing transitional
		}
		return false, 0, nil // supervisord control socket not up yet
	})
}
