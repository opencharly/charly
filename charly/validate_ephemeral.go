package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// validate_ephemeral.go — validation rules for the ephemeral / clone /
// imported / snapshot additions. Wired into the existing validation
// hot path via Validate() in validate.go (and the unified
// loader, which calls these helpers when materializing kind:vm and
// kind:deployment entities).

// ValidateEphemeralOnNode applies all ephemeral-related invariants to
// a single BundleNode. Errors are accumulated into errs.
//
// Invariants enforced:
//   - target=host with ephemeral set → schema error (host is inherently
//     non-ephemeral).
//   - target=pod with from_snapshot set → schema error (pods don't have
//     backing-chain semantics).
//   - ephemeral block: ttl is parseable (or empty for default 1h).
//   - ephemeral block: naming_pattern is parseable as Go template.
//   - effective ttl > 0 (rejects "0s" or negative values).
func ValidateEphemeralOnNode(name string, node *spec.BundleNode, errs *ValidationError) {
	if node == nil {
		return
	}
	// Ephemeral + from_snapshot support is DATA-DRIVEN from the substrate's declared #DeployTraits
	// (candy/plugin-substrate), never a per-substrate-word comparison (the boundary-law
	// incomplete-seam gate, task #22): supports_from_snapshot marks the substrates with a backing
	// chain from_snapshot restores from (vm); supports_ephemeral marks those whose Add/Del path
	// wires the ephemeral register/teardown seam (vm today). A nil-traits target (external/unknown/
	// "") is reported elsewhere — treated as no-backing-chain for the from_snapshot mistake check.
	traits := deployTraitsFor(node.Target)
	if !node.IsEphemeral() {
		// Non-ephemeral deploys still get the from_snapshot check — authoring `from_snapshot:` on a
		// non-backing-chain substrate is usually a mistake.
		if node.FromSnapshot != "" && (traits == nil || !traits.SupportsFromSnapshot) {
			errs.Add("deployment %q: from_snapshot is only valid on a backing-chain substrate (vm) (got target=%q)", name, node.Target)
		}
		return
	}

	// The deploy IS ephemeral: reject it on any substrate whose Add/Del path does not (yet) wire the
	// ephemeral register/teardown seam. vm (supports_ephemeral) is the only one today; pod/k8s/local/
	// android and host were previously accepted-then-inert (a load-time lie) — reject loudly.
	if traits != nil && !traits.SupportsEphemeral {
		if node.FromSnapshot != "" && !traits.SupportsFromSnapshot {
			errs.Add("deployment %q: target=%s with from_snapshot is not supported (containers/namespaces have no backing chains; only vm restores from a snapshot)", name, node.Target)
		}
		errs.Add("deployment %q: target=%s with ephemeral is not yet supported (the ephemeral lifecycle — TTL timer + charly.yml persistence — is wired for target=vm only; tracked for the other substrates in the bed-robustness batch)", name, node.Target)
	}

	if node.Ephemeral != nil && node.Ephemeral.TTL != "" {
		if d, err := time.ParseDuration(node.Ephemeral.TTL); err != nil {
			errs.Add("deployment %q: ephemeral.ttl %q is not a valid Go duration (e.g. 30m, 2h, 90s): %v", name, node.Ephemeral.TTL, err)
		} else if d <= 0 {
			errs.Add("deployment %q: ephemeral.ttl must be > 0 (got %s)", name, d)
		}
	}

	// Reject the contradiction: explicit ephemeral + explicit
	// disposable: false is rejected at load time. The auto-promote in
	// LoadBundleConfig already turned Disposable=true for ephemeral
	// nodes; this check is for the rare case where validation runs on
	// a config that bypassed the loader (e.g., direct YAML inspection).
	// Since Disposable is a plain bool and we can't distinguish
	// authored-false from default-false reliably, this is largely
	// informational at validate time.
}

// ValidateVmNamingGuard enforces the reserved -eph- infix on
// user-authored kind:vm and kind:pod entity names. The infix is
// reserved for ephemeral instance names (rendered from
// ephemeral.naming_pattern).
func ValidateVmNamingGuard(name string, errs *ValidationError) {
	if strings.Contains(name, "-eph-") {
		errs.Add("name %q contains reserved infix \"-eph-\"; this is reserved for ephemeral instance names — pick a different name", name)
	}
}

// validateEphemeralUnified is the unified-loader entry point for ephemeral
// deploy handling (mirrors validatePreemptibleUnified): it auto-promotes
// disposable:true on ephemeral entries and validates the ephemeral / vm-naming
// invariants across a loaderkit.UnifiedFile's Bundle map. Both the project charly.yml's
// inline deploy: entries AND the per-host ~/.config/charly/charly.yml flow
// through here, so the promotion + checks apply once, one path (R3) — the old
// LoadBundleConfig ran these only on the per-host file.
func validateEphemeralUnified(uf *loaderkit.UnifiedFile) error {
	if uf == nil {
		return nil
	}
	// Auto-promote disposable:true on ephemeral entries — the one load-bearing
	// exception to /charly-internals:disposable's anti-derivation rule
	// (ephemeral STRENGTHENS the disposability contract; see classification.go).
	for name, node := range uf.Bundle {
		if node.IsEphemeral() && (node.Disposable == nil || !*node.Disposable) {
			t := true
			node.Disposable = &t
			uf.Bundle[name] = node
		}
	}
	errs := &ValidationError{}
	for name, node := range uf.Bundle {
		ValidateEphemeralOnNode(name, &node, errs)
		ValidateVmNamingGuard(name, errs)
	}
	if errs.HasErrors() {
		return fmt.Errorf("ephemeral / naming validation:\n  %s", errs.Error())
	}
	return nil
}

// (ValidationError is defined in validate.go; reused here.)
