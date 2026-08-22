package spec

import (
	"fmt"
	"strings"
)

// VmDomainIdentity normalizes a deploy/fleet name into the per-deploy VM DOMAIN IDENTITY — the
// token that keys the libvirt domain (charly-<identity>), the per-domain state dir, the managed
// ssh-config alias, and the ssh-port ledger entry (vm:<identity>). It is DISTINCT from the kind:vm
// ENTITY (the disk/spec source, resolved via the deploy's `from:` cross-ref): several distinct beds
// may share one entity, so keying the domain by the ENTITY collided them on one libvirt domain +
// one disk + one host SSH port. Keying by the DEPLOY NAME instead makes distinct beds collision-free
// by construction (each gets its own domain, per-deploy disk overlay, and auto-allocated port).
//
// The normalization strips a leading "vm:" prefix and flattens the instance ("/") and nested-path
// (".") separators to "-", so a bare bed name maps to itself (check-builder-vm → check-builder-vm),
// a fleet ref maps to its VM token (vm:arch → arch), and a direct `charly vm create <entity>` (whose
// domain identity IS the entity) is unchanged. Both the host preresolver and candy/plugin-deploy-vm
// call this on the SAME deploy name, so the domain they derive always agrees.
//
// Home note (FLOOR-legal leaf): lives in package spec — the always-floor-legal
// wire/vocabulary leaf — so BOTH charly core and a plugin (candy/plugin-check's bed_session.go,
// #55 W3 B2-full) derive the domain identity without a vmshared import. stdlib-only (strings).
func VmDomainIdentity(deployName string) string {
	id := strings.TrimPrefix(deployName, "vm:")
	id = strings.ReplaceAll(id, "/", "-")
	id = strings.ReplaceAll(id, ".", "-")
	return id
}

// VmSshAlias returns the canonical managed-ssh-config Host alias for a VM deployment name
// ("charly-" namespaced) — the token `charly vm create` / the vm deploy lifecycle writes the
// Host stanza under (~/.config/charly/ssh_config) and an SSHExecutor built with just
// `Host: VmSshAlias(id)` lets ssh(1) supply User/Port/IdentityFile. Pure naming leaf, a peer of
// VmDomainIdentity: lives in package spec — the always-floor-legal wire/vocabulary leaf — so the
// spec/exec executor-chain constructors (RootExecutorForDeployNode/ResolveDeployChain/
// VmChildExecutor) derive the alias without an sdk/kit import (#55 K4). sdk/kit re-exports it for
// the host-coupled ssh-config-fragment writers (renderStanza/VmSshStanza) that STAY in kit.
func VmSshAlias(vmName string) string {
	return "charly-" + vmName
}

// VmNameFromDeployName extracts the VM entity name from a deploy-key in the legacy
// "vm:<name>[/<instance>]" form. Callers that hold a schema-v4 deploy key (whose entity comes from
// the node's `vm:` field) resolve the entity a different way (the node's own From field); this
// helper handles the prefixed form (legacy refs + the "vm:<entity>" key the del path builds for
// ledger/teardown keying). The `instance` suffix is preserved for future per-instance addressing
// but currently unused. Relocated from sdk/vmshared (#55 vmshared Bucket B) so kernel-floor charly
// files address VM deploys without a vmshared import; vmshared keeps a re-export forwarder.
func VmNameFromDeployName(deployName string) (string, error) {
	if !strings.HasPrefix(deployName, "vm:") {
		return "", fmt.Errorf("VM deploy name must start with 'vm:' (got %q)", deployName)
	}
	rest := strings.TrimPrefix(deployName, "vm:")
	if rest == "" {
		return "", fmt.Errorf("VM deploy name missing vm-name portion (got %q)", deployName)
	}
	if before, _, ok := strings.Cut(rest, "/"); ok {
		return before, nil
	}
	return rest, nil
}

// SplitVmAddress detects the "vm:"-prefixed CLI ADDRESSING form (`charly fleet add/del
// vm:<name>` / `vm:<parent.child>`) and returns the address with that prefix stripped, plus
// whether it was present. "vm:" here is an ADDRESSING HINT — "resolve this via the vm
// substrate" — NEVER an identity itself; a caller that needs the plain (tree-lookup /
// ledger-identity) form strips it via this helper, one which needs the sanitized dc.Fleet
// key form still applies "vm:"+VmDomainIdentity(...) separately (a DIFFERENT canonical form).
//
// NOT the same job as VmNameFromDeployName (which extracts the VM ENTITY and errors when the
// prefix is ABSENT — a different, already-established, unchanged contract) or VmDomainIdentity
// (which sanitizes dots/slashes for a domain-identity STRING, unconditionally, prefix or not).
// Relocated from sdk/vmshared (#55 vmshared Bucket B); vmshared keeps a re-export forwarder.
func SplitVmAddress(name string) (plain string, isVm bool) {
	if after, ok := strings.CutPrefix(name, "vm:"); ok {
		return after, true
	}
	return name, false
}
