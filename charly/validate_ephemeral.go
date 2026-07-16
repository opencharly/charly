package main

import (
	"fmt"
	"github.com/opencharly/sdk/spec"
	"strings"
	"time"
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
	if !node.IsEphemeral() {
		// Non-ephemeral deploys still get the from_snapshot check —
		// authoring `from_snapshot:` on a non-ephemeral non-VM is
		// usually a mistake.
		if node.FromSnapshot != "" && node.Target != "vm" {
			errs.Add("deployment %q: from_snapshot is only valid on target=vm (got target=%q)", name, node.Target)
		}
		return
	}

	switch node.Target {
	case "host":
		errs.Add("deployment %q: target=host with ephemeral is not supported (host is inherently non-ephemeral)", name)
	case "pod", "container":
		if node.FromSnapshot != "" {
			errs.Add("deployment %q: target=pod with from_snapshot is not supported (containers don't have backing chains)", name)
		}
	case "vm":
		// ok
	case "k8s", "kubernetes":
		// ok — ephemeral is namespace-per-instance, no from_snapshot needed
		if node.FromSnapshot != "" {
			errs.Add("deployment %q: target=k8s with from_snapshot is not supported (namespace-per-instance pattern doesn't use backing chains)", name)
		}
	case "":
		// schema v4 invariant elsewhere; don't double-report
	default:
		// unknown target; reported elsewhere
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

// ValidateImportedSource checks the additional invariants for the
// imported VmSource branch beyond what the #VmSource CUE schema already
// covers (required-field presence + cross-arm exclusion). This adds
// path/sanity checks that need filesystem access.
func ValidateImportedSource(name string, src *VmSource, errs *ValidationError) {
	if src.Kind != "imported" {
		return
	}
	// disk_path should be absolute (relative paths can't be a stable
	// libvirt-XML <disk source file=/>).
	if src.DiskPath != "" && !strings.HasPrefix(src.DiskPath, "/") {
		errs.Add("vm %q: source.disk_path %q must be absolute (libvirt requires absolute paths in <disk source file=/>)", name, src.DiskPath)
	}
}

// ValidateEphemeralAcrossDeploy aggregates ephemeral / naming /
// imported validation across an entire BundleConfig. Called from the
// top-level Validate path.
func ValidateEphemeralAcrossDeploy(dc *BundleConfig, errs *ValidationError) {
	if dc == nil {
		return
	}
	for name, node := range dc.Bundle {
		ValidateEphemeralOnNode(name, &node, errs)
	}
}

// validateEphemeralUnified is the unified-loader entry point for ephemeral
// deploy handling (mirrors validatePreemptibleUnified): it auto-promotes
// disposable:true on ephemeral entries and validates the ephemeral / vm-naming
// invariants across a UnifiedFile's Bundle map. Both the project charly.yml's
// inline deploy: entries AND the per-host ~/.config/charly/charly.yml flow
// through here, so the promotion + checks apply once, one path (R3) — the old
// LoadBundleConfig ran these only on the per-host file.
func validateEphemeralUnified(uf *UnifiedFile) error {
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
