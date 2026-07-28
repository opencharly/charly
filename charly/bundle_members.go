package main

// bundle_members.go — sibling `peer:` member deployments: the ONE shared lifecycle.
//
// A BundleNode's `peer:` map declares companion deployments brought up
// ALONGSIDE it on the shared `charly` network (NOT nested inside it). The canonical
// case is a Chrome driver pod that CDP-probes a web-server subject via a check
// with `on: <peer>` (see check_members.go); members are reachable by
// `${HOST:<name>}` and are never check-live'd themselves.
//
// The LOAD-half — foldMembers / sortedMemberKeys / sortedDeployKeys plus the venue-flatten pass —
// relocated to sdk/loaderkit (bundle_load.go) per the lead's U1 SPLIT ruling (K1-LOADER
// RELOCATION): they are registry-free pure maps/tree operations the plugin-callable
// loaderkit.LoadUnified wires as seams directly. See loaderkit.FoldMembers / loaderkit.SortedMemberKeys
// / loaderkit.SortedDeployKeys. The DEPLOY-half below (bringUpMembers / tearDownMembers) STAYS
// host-resident: it shells out via proclifecycle.RunCharlySubcommand + reads the live registry
// (nodeTraits), so it is NOT registry-free. bringUpMembers / tearDownMembers are the single shared
// helpers invoked by BOTH the kind:check bed runner (check_bed_run.go) and the operator deploy path
// (bundle_add_cmd.go) — `peer:` works identically for check and deploy from one codebase. This file
// moves with those consumers through their respective seams, never alone.

import (
	"errors"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/proclifecycle"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// validateMembers / validMemberTarget relocated to sdk/loaderkit (bundle_load.go, K1-LOADER
// RELOCATION LOAD-half) — registry-free member-invariant validation over the CUE-derived
// spec.ResourceKinds vocabulary (the SAME set deployTargetWords derives from), reached via the
// LoadSeams.ValidateMembers seam. See loaderkit.ValidateMembers.

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
//
// K4-C WALK PORT (landed): the outer switch dispatches on isVmMember/isPodMember, which read
// the STAMPED DESCENT TRAIT (D-data), not the substrate kind word — and unlike
// deriveChildExecutorForPath, most bodies here shell out via proclifecycle.RunCharlySubcommand (a
// `charly <verb>` re-entrant CLI call, itself running IN this same host process — not the
// HostBuild("cli") reverse-channel reentry an out-of-process plugin would need). This function's
// BODY is UNCHANGED and stays host-side (providerRegistry + ledger + subprocess-dependent); the
// plugin's walk (candy/plugin-bundle/walk.go) reaches it via the narrow "deploy-members-up" seam
// (host_build_deploy_members.go) once, at the end of a successful `charly bundle add` tree walk.
func bringUpMembers(node *spec.BundleNode, imageTag string) error {
	if node == nil || len(node.Members) == 0 {
		return nil
	}
	for _, memberKey := range loaderkit.SortedMemberKeys(node.Members) {
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
			vmshared.StartLibvirtUserSession()
			// The member's libvirt domain is named after the MEMBER deploy (memberKey), not the
			// shared kind:vm entity (memberNode.From) — so member VMs sharing one entity across beds
			// get distinct, collision-free domains + per-domain disk overlays + ports (P33). The
			// entity is the disk/spec source (the `bundle add` ref); --domain names this member's domain.
			memberDomain := spec.VmDomainIdentity(memberKey)
			_ = proclifecycle.RunCharlySubcommand("vm", "destroy", memberNode.From, "--domain", memberDomain, "--if-exists")
			if err := proclifecycle.RunCharlySubcommand("vm", "create", memberNode.From, "--domain", memberDomain); err != nil {
				return fmt.Errorf("peer %q (vm create %s): %w", memberKey, memberNode.From, err)
			}
			deploykit.WaitForVmSshReady(memberDomain)
			if err := proclifecycle.RunCharlySubcommand(withMemberTag([]string{"bundle", "add", memberKey, memberNode.From}, imageTag)...); err != nil {
				return fmt.Errorf("peer %q (vm bundle add): %w", memberKey, err)
			}
			// Same nested-local-child gap the isVM bed root closes: plugin-deploy-vm's
			// PostApply skips target:local children, so deploy them into the guest here.
			if err := deploykit.DeployNestedLocalChildren(memberKey, memberNode.Children, func(childKey, dotted string) error {
				return proclifecycle.RunCharlySubcommand("bundle", "add", dotted)
			}); err != nil {
				return fmt.Errorf("peer %q: %w", memberKey, err)
			}
		case isPodMember(memberNode):
			for _, step := range [][]string{{"config", memberKey}, {"start", memberKey}} {
				if err := proclifecycle.RunCharlySubcommand(withMemberTag(step, imageTag)...); err != nil {
					return fmt.Errorf("peer %q (%v): %w", memberKey, step, err)
				}
			}
			deploykit.WaitForContainerReady(memberKey)
		default:
			// kind:local member — applies candies in place during bundle add.
			if err := proclifecycle.RunCharlySubcommand(withMemberTag([]string{"bundle", "add", memberKey}, imageTag)...); err != nil {
				return fmt.Errorf("peer %q (bundle add): %w", memberKey, err)
			}
		}
	}
	return nil
}

// tearDownMembers tears down every member of `node` in deterministic order — the companion to
// bringUpMembers. It attempts every member and returns their joined errors so callers can finish
// the full cleanup while still failing the owning operation.
//
// K4-C WALK PORT (landed): same as bringUpMembers above — stays host-side unchanged, reached
// via the narrow "deploy-members-down" seam from `charly bundle del`'s plugin-side walk.
func tearDownMembers(node *spec.BundleNode) error {
	if node == nil || len(node.Members) == 0 {
		return nil
	}
	var errs []error
	for _, memberKey := range loaderkit.SortedMemberKeys(node.Members) {
		memberNode := node.Members[memberKey]
		var err error
		switch {
		case isVmMember(memberNode):
			// `vm destroy` removes the libvirt domain (named after the MEMBER deploy, not the shared
			// entity — P33), but bring-up ALSO registered the member in the deploy ledger via
			// `bundle add`. Reverse that too, or a ledger record survives every teardown and they
			// accumulate run over run.
			destroyErr := proclifecycle.RunCharlySubcommand("vm", "destroy", memberNode.From, "--domain", spec.VmDomainIdentity(memberKey), "--if-exists")
			delErr := proclifecycle.RunCharlySubcommand(deploykit.BundleDelArgv(memberKey)...)
			err = errors.Join(destroyErr, delErr)
		case isPodMember(memberNode):
			err = proclifecycle.RunCharlySubcommand("remove", memberKey, "--purge")
		default:
			err = proclifecycle.RunCharlySubcommand(deploykit.BundleDelArgv(memberKey)...)
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
func isPodMember(node *spec.BundleNode) bool {
	return node != nil && nodeTraits(node).Venue == "container"
}

// isVmMember reports whether a folded group member is an SSH-venue (vm) substrate (P9 trait, not
// the kind word), so the group bed builds its disk (vm build) and brings it up via the libvirt
// lifecycle (vm create + ssh-wait) rather than the pod/local path.
func isVmMember(node *spec.BundleNode) bool {
	return node != nil && nodeTraits(node).Venue == "ssh"
}
