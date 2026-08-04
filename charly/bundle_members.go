package main

// bundle_members.go — TRANSITIONAL (#55 W3 A4, dies in the immediately-following B2-full unit):
// sibling `peer:` member deployments.
//
// A BundleNode's `peer:` map declares companion deployments brought up
// ALONGSIDE it on the shared `charly` network (NOT nested inside it). The canonical
// case is a Chrome driver pod that CDP-probes a web-server subject via a check
// with `on: <peer>` (the cross-deployment `on:`/`${HOST:}` resolution lives
// plugin-side in candy/plugin-check/members.go); members are reachable by
// `${HOST:<name>}` and are never check-live'd themselves.
//
// The LOAD-half — foldMembers / sortedMemberKeys / sortedDeployKeys plus the venue-flatten pass —
// relocated to sdk/loaderkit (bundle_load.go) per the lead's U1 SPLIT ruling (K1-LOADER
// RELOCATION). The DEPLOY-half below (bringUpMembers / tearDownMembers) has ALSO now relocated —
// canonically, to sdk/deploykit.BringUpMembers/TearDownMembers (#55 W3 A4's R3 3-way audit
// found the venue-classification predicate, not the whole engine, was the real duplication risk;
// resolved by promoting spec.IsVmVenue/IsContainerVenue, mirroring spec.HostRooted). The operator
// deploy path (candy/plugin-bundle/walk.go) already calls the sdk/deploykit copy directly — the
// former "deploy-members-up"/"deploy-members-down" HostBuild seam is DELETED.
//
// This file's copy is a TEMPORARY DUPLICATE kept for exactly ONE reason: host_build_check_bed.go's
// members-up/members-down ops still call these as a same-package function (core cannot import
// sdk/deploykit — import-purity). B2-full (the very next unit, landing in the same working
// session) deletes this file entirely once candy/plugin-check calls sdk/deploykit directly
// instead, alongside its flock/lease/env dissolution. DO NOT add new callers to this copy or
// extend it — it is scheduled to die, not to become a second permanent home.

import (
	"errors"
	"fmt"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/hostenv"
	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"
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
// deriveChildExecutorForPath, most bodies here shell out via proc.RunCharlySubcommand (a
// `charly <verb>` re-entrant CLI call, itself running IN this same host process — not the
// HostBuild("cli") reverse-channel reentry an out-of-process plugin would need). This function's
// BODY is UNCHANGED. The operator path (candy/plugin-bundle/walk.go) now calls
// sdk/deploykit.BringUpMembers directly (#55 W3 A4 — the former "deploy-members-up" seam is
// deleted); ONLY host_build_check_bed.go's members-up op still reaches THIS copy, as a
// same-package call (see the file header — this copy dies with B2-full).
func bringUpMembers(node *spec.BundleNode, imageTag string) error {
	if node == nil || len(node.Members) == 0 {
		return nil
	}
	for _, memberKey := range spec.SortedMemberKeys(node.Members) {
		memberNode := node.Members[memberKey]
		// The member's deploy-shaped override PERSIST (port / volume / env / security / network +
		// the resource-arbitration role) moved PLUGIN-SIDE to candy/plugin-check's bed runner (bed
		// path, from CheckBedReply.NodeJSON's nested peer map) and candy/plugin-bundle's walk
		// (operator path, from rootNode.Members) — #55 coneC-dsh β1. The plugin calls
		// deploykit.PersistBedDeployOverrides BEFORE this host `members-up`/`deploy-members-up` seam
		// fires, so the member's declared port: publishes + its preemptible/requires_exclusive role
		// is seeded by the time the member's own `charly config`/`charly start` runs here (the
		// group live-preemption shape — see check-preempt-live-pod). A member node is NON-disposable
		// (foldMembers marks only the folded top-level copy), so this never writes a disposable bed
		// the overlay's validateCheckBeds would reject.
		switch {
		case isVmMember(memberNode):
			// VM member: full libvirt lifecycle, mirroring the isVM bed root
			// (check_bed_run.go). The VM disk is built by the caller's build step
			// (the group bed's build arm); here we (re)create + wait for ssh +
			// deploy the VM node — `bundle add <member> <vm-entity>` (the VM-template
			// ref, like the isVM root's deploy-add), not the bare pod/local form.
			// Best-effort pre-destroy clears a stale domain from an interrupted run.
			hostenv.StartLibvirtUserSession()
			// The member's libvirt domain is named after the MEMBER deploy (memberKey), not the
			// shared kind:vm entity (memberNode.From) — so member VMs sharing one entity across beds
			// get distinct, collision-free domains + per-domain disk overlays + ports (P33). The
			// entity is the disk/spec source (the `bundle add` ref); --domain names this member's domain.
			memberDomain := spec.VmDomainIdentity(memberKey)
			_ = proc.RunCharlySubcommand("vm", "destroy", memberNode.From, "--domain", memberDomain, "--if-exists")
			if err := proc.RunCharlySubcommand("vm", "create", memberNode.From, "--domain", memberDomain); err != nil {
				return fmt.Errorf("peer %q (vm create %s): %w", memberKey, memberNode.From, err)
			}
			specexec.WaitForVmSshReady(memberDomain)
			if err := proc.RunCharlySubcommand(withMemberTag([]string{"bundle", "add", memberKey, memberNode.From}, imageTag)...); err != nil {
				return fmt.Errorf("peer %q (vm bundle add): %w", memberKey, err)
			}
			// Same nested-local-child gap the isVM bed root closes: plugin-deploy-vm's
			// PostApply skips target:local children, so deploy them into the guest here.
			if err := spec.DeployNestedLocalChildren(memberKey, memberNode.Children, func(childKey, dotted string) error {
				return proc.RunCharlySubcommand("bundle", "add", dotted)
			}); err != nil {
				return fmt.Errorf("peer %q: %w", memberKey, err)
			}
		case isPodMember(memberNode):
			for _, step := range [][]string{{"config", memberKey}, {"start", memberKey}} {
				if err := proc.RunCharlySubcommand(withMemberTag(step, imageTag)...); err != nil {
					return fmt.Errorf("peer %q (%v): %w", memberKey, step, err)
				}
			}
			specexec.WaitForContainerReady(memberKey)
		default:
			// kind:local member — applies candies in place during bundle add.
			if err := proc.RunCharlySubcommand(withMemberTag([]string{"bundle", "add", memberKey}, imageTag)...); err != nil {
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
// K4-C WALK PORT (landed): same as bringUpMembers above — this copy's BODY is unchanged, but the
// operator path now calls sdk/deploykit.TearDownMembers directly (#55 W3 A4 — the former
// "deploy-members-down" seam is deleted); ONLY host_build_check_bed.go's members-down op still
// reaches THIS copy (dies with B2-full).
func tearDownMembers(node *spec.BundleNode) error {
	if node == nil || len(node.Members) == 0 {
		return nil
	}
	var errs []error
	for _, memberKey := range spec.SortedMemberKeys(node.Members) {
		memberNode := node.Members[memberKey]
		var err error
		switch {
		case isVmMember(memberNode):
			// `vm destroy` removes the libvirt domain (named after the MEMBER deploy, not the shared
			// entity — P33), but bring-up ALSO registered the member in the deploy ledger via
			// `bundle add`. Reverse that too, or a ledger record survives every teardown and they
			// accumulate run over run.
			destroyErr := proc.RunCharlySubcommand("vm", "destroy", memberNode.From, "--domain", spec.VmDomainIdentity(memberKey), "--if-exists")
			delErr := proc.RunCharlySubcommand(spec.BundleDelArgv(memberKey)...)
			err = errors.Join(destroyErr, delErr)
		case isPodMember(memberNode):
			err = proc.RunCharlySubcommand("remove", memberKey, "--purge")
		default:
			err = proc.RunCharlySubcommand(spec.BundleDelArgv(memberKey)...)
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
