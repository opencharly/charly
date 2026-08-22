package spec

import (
	"fmt"
	"strings"
)

// deploy_tree_validate.go — pure, kind-blind structural validation over an
// already-merged deployments tree (map[string]FleetNode), relocated from
// charly/unified.go (FLOOR-SLIM K1-proper mechanical batch). Every function
// here operates ONLY on FleetNode — no registry, no *UnifiedFile, no host
// I/O — so it belongs beside the wire type it validates (the same D-clause
// precedent as node_helpers.go's ClassifyDoc). The loader's validation chain calls
// ValidateDeploymentTree(merged.Fleet) directly; fleet_members.go calls
// ValidateDeploymentName for a folded peer member's key.

// ValidateDeploymentTree enforces structural invariants on the deployments tree
// that can't be expressed in the YAML struct tags:
//
//   - Map keys at every level MUST NOT contain "." (dots are reserved
//     for dotted-path CLI addressing like `charly fleet add a.b.c`).
//   - Every explicit pod deploy must declare `box:` (ValidateDeployRequiresBox).
//
// Errors include the offending path so the user sees exactly which entry needs
// to be fixed.
func ValidateDeploymentTree(deploy map[string]FleetNode) error {
	if deploy == nil {
		return nil
	}
	for name, node := range deploy {
		if err := ValidateDeploymentName(name, ""); err != nil {
			return err
		}
		if err := ValidateDeploymentChildren(name, &node); err != nil {
			return err
		}
	}
	if err := ValidateDeployRequiresBox(deploy); err != nil {
		return err
	}
	return nil
}

// ValidateDeployRequiresBox enforces the 2026-05-12 schema rule:
// every `target: pod` deploy entry MUST declare its `box:` field.
// Pre-cutover the check runner silently fell back to inspecting the
// running container's image ref via `containerImageRef`, which read
// stale OCI labels off volume-pinned containers and dropped any
// probes added after the seed image. The hard-required field forces
// operator intent to be explicit; the check runner now resolves the
// ref ONLY from this field.
//
// Scope: target: pod (or empty — pod is the default). target: vm
// uses `vm:`, target: local is candy-driven, target: kubernetes
// CLUSTER definitions live in the `kubernetes:` section (not deploy:).
//
// Remediation: `charly migrate` (idempotent) walks every
// affected deploy and injects the field, inferring the value from
// the deploy key (`<base>` for `<base>/<instance>` keys; the key
// itself otherwise).
func ValidateDeployRequiresBox(deploy map[string]FleetNode) error {
	for name, node := range deploy {
		// An iterate: benchmark (the former kind:score) composes its scored
		// subject via plan `include:` steps + the iterate.sandbox, NOT a single
		// `box:`. It is exempt from the pod-target box requirement; its own
		// invariants are checked by validateCheckBeds (iterate block validation).
		if node.Iterate != nil {
			continue
		}
		// An agent-provisioned member carries NO box: by design — the AI builds
		// its image at run time (the iterate-benchmark contract). Exempt it from
		// the pod-target box requirement.
		if node.AgentProvisioned {
			continue
		}
		target := node.Target
		// Only an explicit pod-target (a `pod` node, or a `fleet` that inferred pod
		// from a box) is box-required. An EMPTY target is a group / per-host overlay
		// entry (no workload), never a pod-leaf — in node-form a real pod always
		// carries its box (the target is inferred FROM the box), so an empty target
		// can only be a group, which needs no box.
		if target != "pod" {
			continue
		}
		if node.Image == "" {
			// A fleet GROUP / venue (no own workload) carries members but no
			// box of its own — its member nodes each declare their box and are
			// validated as folded top-level entries. Only a LEAF pod-workload
			// (no members) must declare box.
			if len(node.Members) > 0 || len(node.Children) > 0 {
				continue
			}
			return fmt.Errorf(
				"deploy entry %q lacks required `box:` field — a pod-target deploy must declare `box:` explicitly (the check runner reads the operator's declared intent, not the running container's stale label)",
				name,
			)
		}
	}
	return nil
}

// ValidateDeploymentChildren recurses ValidateDeploymentName over node's
// nested-deployment children.
func ValidateDeploymentChildren(path string, node *FleetNode) error {
	if node == nil || len(node.Children) == 0 {
		return nil
	}
	for childName, child := range node.Children {
		childPath := childName
		if path != "" {
			childPath = path + "." + childName
		}
		if err := ValidateDeploymentName(childName, path); err != nil {
			return err
		}
		if err := ValidateDeploymentChildren(childPath, child); err != nil {
			return err
		}
	}
	return nil
}

// ValidateDeploymentName rejects a deploy-tree key containing ".", reserved
// for dotted-path CLI addressing (`charly fleet add a.b.c`).
func ValidateDeploymentName(name, parentPath string) error {
	full := name
	if parentPath != "" {
		full = parentPath + "." + name
	}
	if strings.Contains(name, ".") {
		// This gate runs against BOTH authored charly.yml entries AND machine-written per-host
		// overlay entries (RCA #6, FINAL/K5 unit 6a) — a prior message revision assumed only a
		// human authored the offending key and told them to "Rename this entry," which is wrong
		// advice for an entry a writer bug produced (nothing to manually rename; the fix is the
		// writer). Kept source-agnostic: names the constraint, not a remedy that only fits one case.
		return fmt.Errorf(
			"deployment key %q contains '.' — the character is reserved for dotted-path addressing (charly fleet add a.b.c), never a literal deploy-tree key",
			full,
		)
	}
	return nil
}
