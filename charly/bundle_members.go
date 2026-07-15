package main

// bundle_members.go — sibling `peer:` member deployments: the ONE shared lifecycle.
//
// A BundleNode's `peer:` map declares companion deployments brought up
// ALONGSIDE it on the shared `charly` network (NOT nested inside it). The canonical
// case is a Chrome driver pod that CDP-probes a web-server subject via a check
// with `on: <peer>` (see check_members.go); members are reachable by
// `${HOST:<name>}` and are never check-live'd themselves.
//
// foldMembers registers each member as a top-level, addressable Bundle entry at
// load time (inheriting the owner's disposability), so a member is brought
// up/torn down by the SAME `charly config`/`charly start`/`charly remove` verbs the deploy
// path already uses — no parallel bring-up logic (R3). bringUpMembers /
// tearDownMembers are the single shared helpers, invoked by BOTH the kind:check
// bed runner (check_bed_run.go) and the operator deploy path
// (bundle_add_cmd.go) — `peer:` works identically for check and deploy from one
// codebase.

import (
	"errors"
	"fmt"
	"sort"
)

// foldMembers copies every deploy node's `peer:` entries into the Bundle map as
// top-level addressable entries (MemberOf set, disposability inherited), so every
// deploy verb resolves a member by name through the same path as any deploy.
// Runs BEFORE validateDeploymentTree (so folded members get the same deploy
// validation); a check bed is itself a `disposable: true` bundle, so a bed's members
// fold the same way. A member name colliding with any existing deploy/member entry is
// a hard error.
func foldMembers(uf *UnifiedFile) error {
	if uf == nil || len(uf.Bundle) == 0 {
		return nil
	}
	// Collect first (we mutate the map below). Iterate a sorted owner list so
	// a collision between two owners' members is reported deterministically.
	type pendingMember struct {
		key        string
		node       BundleNode
		owner      string
		disposable bool
	}
	var pending []pendingMember
	for _, owner := range sortedDeployKeys(uf.Bundle) {
		ownerNode := uf.Bundle[owner]
		for _, memberKey := range sortedMemberKeys(ownerNode.Members) {
			memberNode := ownerNode.Members[memberKey]
			if memberNode == nil {
				return fmt.Errorf("deploy %q peer %q is empty", owner, memberKey)
			}
			// An agent-provisioned member is deployed by the AI at run time (the
			// iterate-benchmark contract), NOT by the bed/deploy. Skip it: never a
			// top-level addressable entry → no auto bring-up, and no cross-bed name
			// collision (the same venue name, e.g. `os`, recurs across iterate
			// beds). The scorer reaches its `charly-<name>` container via
			// resolveScoringChain's bare-name fallback.
			if memberNode.AgentProvisioned {
				continue
			}
			pending = append(pending, pendingMember{
				key:        memberKey,
				node:       *memberNode,
				owner:      owner,
				disposable: ownerNode.IsDisposable(),
			})
		}
	}
	for _, p := range pending {
		if _, clash := uf.Bundle[p.key]; clash {
			return fmt.Errorf(
				"peer name %q (declared under deploy %q) collides with an existing deploy/bed/peer entry — peer names must be globally unique; rename it",
				p.key, p.owner)
		}
		node := p.node
		node.MemberOf = p.owner
		// A companion inherits its owner's disposability so the owner's
		// teardown/rebuild (e.g. a kind:check bed's charly update) is authorized to
		// destroy + rebuild it too.
		if p.disposable {
			disposable := true
			node.Disposable = &disposable
		}
		uf.Bundle[p.key] = node
	}
	return nil
}

// validateMembers enforces the member-specific invariants beyond the generic deploy
// validation (which already runs on the folded members): member keys carry no `.`
// (dots are reserved for nested dotted-path addressing) and reference a valid
// target kind. Pod-target members get the required-image: check via the generic
// validateDeploymentTree on the folded entry.
func validateMembers(uf *UnifiedFile) error {
	if uf == nil {
		return nil
	}
	for _, owner := range sortedDeployKeys(uf.Bundle) {
		node := uf.Bundle[owner]
		for _, memberKey := range sortedMemberKeys(node.Members) {
			if err := validateDeploymentName(memberKey, owner+" (peer)"); err != nil {
				return err
			}
			memberNode := node.Members[memberKey]
			if memberNode == nil {
				continue
			}
			// Kind-blind: a peer member's target is valid iff it is a recognized
			// deploy substrate (the empty target defaults to pod). Dispatched
			// through the recognition registry — NOT a compiled-in per-kind switch —
			// because the substrate kinds are plugin-served (C2-substrate), so a new
			// external deploy substrate is a valid member target without a core edit
			// (the kernel/plugin boundary law: a validator checks the word is a
			// recognized kind; the P9 word-switch gate's intended pattern).
			if !validMemberTarget(memberNode.Target) {
				return fmt.Errorf("deploy %q peer %q has unsupported target %q (not a recognized deploy substrate; \"\" defaults to pod)", owner, memberKey, memberNode.Target)
			}
		}
	}
	return nil
}

// validMemberTarget reports whether target is a valid peer-member deploy target:
// the empty target (which defaults to pod) or one of the canonical deploy
// substrates. It consults the canonical deploy-target set (deployTargetWords —
// the bijection's D-data, tied to spec.ResourceKinds), NOT a compiled-in per-kind
// switch on the consumer, so the consumer names no concrete kind word (the
// kernel/plugin boundary law). A new deploy substrate is added to that canonical
// set once, not to a per-consumer switch. deployTargetWords itself is tracked
// migration inventory (a per-kind Go slice) with a named K-wave exit, not
// permanent core; this cutover removes the consumer-side switch only.
func validMemberTarget(target string) bool {
	if target == "" {
		return true
	}
	for _, w := range deployTargetWords {
		if w == target {
			return true
		}
	}
	return false
}

// sortedMemberKeys returns the member keys of a node in deterministic order.
func sortedMemberKeys(members map[string]*BundleNode) []string {
	if len(members) == 0 {
		return nil
	}
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// withMemberTag appends `--tag <imageTag>` to a member deploy argv when imageTag
// is non-empty (a bed run's per-run tag #75). Empty on the operator bring-up path,
// where members resolve their images normally. One appender (R3).
func withMemberTag(args []string, imageTag string) []string {
	if imageTag == "" {
		return args
	}
	return append(args, "--tag", imageTag)
}

// bringUpMembers brings up every member of `node` ALONGSIDE the (already-deployed)
// owner, in deterministic order, on the shared `charly` network. Each member is a
// folded top-level deploy entry, so bring-up reuses the standard pod pipeline
// verbatim: persist the member's declared deploy overrides (so its declared
// `port:` actually publishes — `charly config` otherwise sources ports from image
// labels behind an operator -p), then `charly config <member>` + `charly start <member>`,
// then wait for readiness. A VM member (target: vm) gets the full libvirt
// lifecycle (create + ssh-wait + deploy), a kind:local member is registered via
// `charly bundle add <member>`. The SAME helper serves the kind:check bed runner
// and the operator deploy path (R3). Idempotent on an already-running member.
func bringUpMembers(node *BundleNode, imageTag string) error {
	if node == nil || len(node.Members) == 0 {
		return nil
	}
	for _, memberKey := range sortedMemberKeys(node.Members) {
		memberNode := node.Members[memberKey]
		// Seed the per-host charly.yml with the member's deploy-shaped overrides
		// (port / volume / env / security / network) so its declared port:
		// publishes to the host — the cross-deployment cdp/vnc/mcp probe reaches
		// the driver via that host-published port. This ALSO seeds the member's
		// resource-arbitration role (preemptible holder / requires_exclusive
		// claimant), so a preemptible-holder member + a requires_exclusive-claimant
		// member drive the arbiter through the member's own `charly start` (the
		// group live-preemption shape — see check-preempt-live-pod). A member node
		// is NON-disposable (foldMembers marks only the folded top-level copy), so
		// this never writes a disposable bed the overlay's validateCheckBeds would
		// reject.
		persistBedDeployOverrides(memberKey, *memberNode)
		switch {
		case isVmMember(memberNode):
			// VM member: full libvirt lifecycle, mirroring the isVM bed root
			// (check_bed_run.go). The VM disk is built by the caller's build step
			// (the group bed's build arm); here we (re)create + wait for ssh +
			// deploy the VM node — `bundle add <member> <vm-entity>` (the VM-template
			// ref, like the isVM root's deploy-add), not the bare pod/local form.
			// Best-effort pre-destroy clears a stale domain from an interrupted run.
			startLibvirtUserSession()
			// The member's libvirt domain is named after the MEMBER deploy (memberKey), not the
			// shared kind:vm entity (memberNode.From) — so member VMs sharing one entity across beds
			// get distinct, collision-free domains + per-domain disk overlays + ports (P33). The
			// entity is the disk/spec source (the `bundle add` ref); --domain names this member's domain.
			memberDomain := vmDomainIdentity(memberKey)
			_ = runCharlySubcommand("vm", "destroy", memberNode.From, "--domain", memberDomain, "--if-exists")
			if err := runCharlySubcommand("vm", "create", memberNode.From, "--domain", memberDomain); err != nil {
				return fmt.Errorf("peer %q (vm create %s): %w", memberKey, memberNode.From, err)
			}
			waitForVmSshReady(memberDomain)
			if err := runCharlySubcommand(withMemberTag([]string{"bundle", "add", memberKey, memberNode.From}, imageTag)...); err != nil {
				return fmt.Errorf("peer %q (vm bundle add): %w", memberKey, err)
			}
			// Same nested-local-child gap the isVM bed root closes: plugin-deploy-vm's
			// PostApply skips target:local children, so deploy them into the guest here.
			if err := deployNestedLocalChildren(memberKey, memberNode.Children, func(childKey, dotted string) error {
				return runCharlySubcommand("bundle", "add", dotted)
			}); err != nil {
				return fmt.Errorf("peer %q: %w", memberKey, err)
			}
		case isPodMember(memberNode):
			for _, step := range [][]string{{"config", memberKey}, {"start", memberKey}} {
				if err := runCharlySubcommand(withMemberTag(step, imageTag)...); err != nil {
					return fmt.Errorf("peer %q (%v): %w", memberKey, step, err)
				}
			}
			waitForContainerReady(memberKey)
		default:
			// kind:local member — applies candies in place during bundle add.
			if err := runCharlySubcommand(withMemberTag([]string{"bundle", "add", memberKey}, imageTag)...); err != nil {
				return fmt.Errorf("peer %q (bundle add): %w", memberKey, err)
			}
		}
	}
	return nil
}

// tearDownMembers tears down every member of `node` in deterministic order — the companion to
// bringUpMembers. It attempts every member and returns their joined errors so callers can finish
// the full cleanup while still failing the owning operation.
func tearDownMembers(node *BundleNode) error {
	if node == nil || len(node.Members) == 0 {
		return nil
	}
	var errs []error
	for _, memberKey := range sortedMemberKeys(node.Members) {
		memberNode := node.Members[memberKey]
		var err error
		switch {
		case isVmMember(memberNode):
			// `vm destroy` removes the libvirt domain (named after the MEMBER deploy, not the shared
			// entity — P33), but bring-up ALSO registered the member in the deploy ledger via
			// `bundle add`. Reverse that too, or a ledger record survives every teardown and they
			// accumulate run over run.
			destroyErr := runCharlySubcommand("vm", "destroy", memberNode.From, "--domain", vmDomainIdentity(memberKey), "--if-exists")
			delErr := runCharlySubcommand(deployDelArgv(memberKey)...)
			err = errors.Join(destroyErr, delErr)
		case isPodMember(memberNode):
			err = runCharlySubcommand("remove", memberKey, "--purge")
		default:
			err = runCharlySubcommand(deployDelArgv(memberKey)...)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("peer %q teardown: %w", memberKey, err))
		}
	}
	return errors.Join(errs...)
}

// isPodMember reports whether a member node is a CONTAINER-venue (pod) deployment — reading the
// stamped descent trait (P9), never the substrate kind word (an empty target resolves to the pod
// default via nodeTraits). Pod members go through config+start; other venues through deploy add.
func isPodMember(node *BundleNode) bool {
	return node != nil && nodeTraits(node).Venue == "container"
}

// isVmMember reports whether a folded group member is an SSH-venue (vm) substrate (P9 trait, not
// the kind word), so the group bed builds its disk (vm build) and brings it up via the libvirt
// lifecycle (vm create + ssh-wait) rather than the pod/local path.
func isVmMember(node *BundleNode) bool {
	return node != nil && nodeTraits(node).Venue == "ssh"
}
