package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// CheckEndpoint + the host-endpoint resolver (the former containerPublishedAddr / sshForwardEndpoint
// bodies + the venue.Kind switch) relocated to sdk/kit (check_endpoint.go) as kit.CheckEndpoint +
// kit.EndpointForVenue(spec.VenueDescriptor, port) — a KIND-BLIND resolver dispatched by the
// descriptor's generic transport word (container/ssh/shell), so the ONE mechanism serves core AND
// plugin-check (R3). The host retains only the venue→descriptor PROJECTION (CheckVenue.Descriptor,
// stamped at construction) and the tunnel-cleanup OWNERSHIP (the caller registers ep.Close +
// drains it after the nested Invoke — the ssh -L forward must outlive the plugin's dial;
// provider_checkenv.go's endpointCleanups + defer runEndpointCleanups).

// resolveCheckEndpoint returns a host-reachable address for the given in-venue TCP port. The caller
// MUST Close() the returned endpoint when done (no-op for container/shell venues; tears down the ssh
// forward for a vm/ssh-host venue). The venue.Kind switch is GONE — resolveCheckVenue stamps the
// venue's generic transport Descriptor at construction, and kit.EndpointForVenue dispatches on it.
func resolveCheckEndpoint(venue *CheckVenue, port int) (*kit.CheckEndpoint, error) {
	return kit.EndpointForVenue(venue.Descriptor, port)
}

// venueRunSilent runs a command on the venue discarding output, returning an
// error on non-zero exit (availability probes + fire-and-forget actions).
func venueRunSilent(ex deploykit.DeployExecutor, script string) error {
	_, _, exit, err := ex.RunCapture(context.Background(), script)
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("command exited %d", exit)
	}
	return nil
}

// venueHasTool reports whether `tool` is on PATH on the venue.
func venueHasTool(ex deploykit.DeployExecutor, tool string) bool {
	_, _, exit, err := ex.RunCapture(context.Background(), "command -v "+tool+" >/dev/null 2>&1")
	return err == nil && exit == 0
}

// check_venue.go — the single venue resolver shared by every `charly check` verb.
//
// The declarative runner (`charly check live`) and the former in-core interactive verbs
// (the live-container probes, all now externalized to out-of-process plugins) historically
// diverged: the runner classified the target (container / VM / local / nested) and built a
// DeployExecutor chain, while the interactive verbs hardcoded `resolveContainer()` +
// `podman exec`, so they only ever worked against a running container — a VM target was
// impossible.
//
// resolveCheckVenue is the ONE classifier + executor builder both paths use, so the host's
// executor (attached to an EXEC-based external verb over the reverse channel) and the
// host-side endpoint pre-resolution (a port-based external verb) work identically against a
// container, a VM (over the
// managed ssh-config alias), an ssh/local host, or a dotted nested path — the
// "same underlying mechanism" guarantee. The verbs then run their tool
// invocations through venue.Exec.RunCapture / GetFile / PutFile, exactly like
// the declarative runner already probes every venue through RunCapture.

// CheckVenue carries a resolved execution venue for an `charly check` verb target.
//
// Exec is the DeployExecutor every verb runs its tool commands through.
// Kind mirrors DeployExecutor.Kind() ("container" | "vm" | "host"). Engine and
// Name are set only for the container venue (some verbs still need the raw
// `podman port` / container name for host-side port mapping); they are empty
// for VM / host venues, which reach published ports via an SSH local-forward
// instead.
type CheckVenue struct {
	Exec     deploykit.DeployExecutor
	Kind     string
	Engine   string // container engine ("podman"/"docker"); "" for vm/host
	Name     string // container name (container venue) or vm entity name (vm venue)
	Instance string
	VMName   string // kind:vm entity name when Kind == "vm"; "" otherwise
	// Descriptor is the venue's GENERIC transport descriptor (container/ssh/shell), stamped at
	// construction and consumed by the kind-blind kit.EndpointForVenue (resolveCheckEndpoint) — the
	// venue's classification (Kind) maps to a transport word HERE, so endpoint resolution never
	// switches on the deploy-kind.
	Descriptor spec.VenueDescriptor
}

// IsContainer reports whether the venue is a running container — the only
// venue where `podman port` host-side mapping applies.
func (v *CheckVenue) IsContainer() bool { return v != nil && v.Kind == "container" }

// resolveCheckVenue maps an `charly check` verb's <name> argument to an execution
// venue, mirroring the live-check dispatch order (candy/plugin-check/live_gather.go's
// pluginCheckRunLive) so the SAME name resolves the SAME way for declarative and interactive
// verbs:
//
//	"."                         → the local host (ShellExecutor).
//	kind:vm entity / target:vm  → SSHExecutor over the managed charly-<vm> alias
//	  deploy / dotted vm child     (or a ResolveDeployChain chain when dotted).
//	target:local deploy         → ShellExecutor / SSHExecutor per host: field.
//	otherwise                   → a running container (ContainerChain).
//
// A missing/unreadable charly.yml is not fatal — name simply falls through
// to the container path (matching checkVmTarget returning false on a
// load error).
func resolveCheckVenue(name, instance string) (*CheckVenue, error) {
	// "." is the local host — lets a check verb run in-place against the host venue,
	// which is also the in-guest delegation target (host SSHes into the VM
	// and runs `charly check live . …` there with the live session env).
	if name == "." {
		return &CheckVenue{Exec: kit.ShellExecutor{}, Kind: "host", Descriptor: spec.VenueDescriptor{Kind: "shell"}}, nil
	}

	dir, _ := os.Getwd()
	if uf, ok, err := LoadUnified(dir); err == nil && ok && uf != nil {
		if domainID, isVM := checkVmTarget(uf, name); isVM {
			var exec deploykit.DeployExecutor = &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 10}
			// Dotted nested path (vm.inner-pod): build the full chain so the
			// verb lands inside the leaf's venue, not the parent VM's shell.
			if strings.Contains(name, ".") {
				if roots, _ := resolveTreeRoot(dir); roots != nil {
					if _, chain, chainErr := deploykit.ResolveDeployChain(roots, name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
						exec = chain
					}
				}
			}
			return &CheckVenue{Exec: exec, Kind: "vm", Name: domainID, VMName: domainID, Instance: instance,
				Descriptor: spec.VenueDescriptor{Kind: "ssh", Host: kit.VmSshAlias(domainID), ConnectTimeout: 10}}, nil
		}
		if node, isLocal := checkLocalTarget(uf, name); isLocal {
			exec, err := deploykit.RootExecutorForDeployNode(&node)
			if err != nil {
				return nil, err
			}
			return &CheckVenue{Exec: exec, Kind: "host", Name: name, Instance: instance,
				Descriptor: kit.DescriptorFromExecutor(exec)}, nil
		}
		// Dotted pod-in-pod path (e.g. cache.migrate): build the full nested
		// chain so the verb lands inside the leaf pod, not the root container.
		// (The dotted VM case is handled above; this is the non-VM nesting the
		// position-derived venue produces.) The leaf container is
		// `charly-<flat-path>` (NestedContainerName) for host-side port mapping.
		if strings.Contains(name, ".") {
			if roots, _ := resolveTreeRoot(dir); roots != nil {
				if _, chain, chainErr := deploykit.ResolveDeployChain(roots, name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
					return &CheckVenue{
						Exec:       chain,
						Kind:       "container",
						Engine:     "podman",
						Name:       "charly-" + kit.NestedContainerName(name),
						Instance:   instance,
						Descriptor: spec.VenueDescriptor{Kind: "container", Engine: "podman", ContainerName: "charly-" + kit.NestedContainerName(name)},
					}, nil
				}
			}
		}
	}

	// Default: a running container.
	engine, containerName, err := deploykit.ResolveContainer(name, instance)
	if err != nil {
		return nil, err
	}
	return &CheckVenue{
		Exec:       deploykit.ContainerChain(engine, containerName),
		Kind:       "container",
		Engine:     engine,
		Name:       containerName,
		Instance:   instance,
		Descriptor: spec.VenueDescriptor{Kind: "container", Engine: engine, ContainerName: containerName},
	}, nil
}

// resolveLeafVenue walks a (possibly dotted) name to its LEAF node — via the same
// tree-walk resolveDeployNodeByPath already uses for dispatch/del — and reports the
// LEAF's own venue trait. This answers "does the node the FULL path actually names
// resolve as venue X", which is a different question than checking only the dotted
// path's ROOT segment (the delegate-into-guest shape: "is the ROOT a venue we SSH
// into, addressing something nested inside its guest"). RCA #12 (FINAL/K5 unit 6a):
// checkVmTarget/checkLocalTarget originally checked ONLY the root, so a VM/local
// venue nested as a Children entry UNDER a non-matching-venue parent (e.g. a target:vm
// deploy nested under a target:pod parent — check-sidecar-pod.check-sidecar-pod-ephvm,
// the first bed to put a vm in that position) was invisible to both classifiers and
// fell through to the container default. Returns ok=false if the tree walk can't
// resolve `name` to a real node (unknown name, orphaned dotted path, undotted name).
func resolveLeafVenue(uf *loaderkit.UnifiedFile, name string) (node spec.BundleNode, venue string, ok bool) {
	if uf == nil || uf.Bundle == nil || !strings.Contains(name, ".") {
		return spec.BundleNode{}, "", false
	}
	n, found := resolveDeployNodeByPath(uf.Bundle, name)
	if !found || n == nil {
		return spec.BundleNode{}, "", false
	}
	return *n, nodeTraits(n).Venue, true
}

// checkVmTarget reports whether `name` resolves to a VM venue and, if so, the per-deploy DOMAIN
// IDENTITY to SSH into (charly-<domainID> is the live libvirt domain + managed ssh alias). Covers a
// direct kind:vm entity (its own name IS the domain identity), a kind:deployment with target:vm (the
// deploy name, NOT its vm: entity — the domain is named after the deploy, P33), a dotted path whose
// LEAF resolves to a target:vm deployment nested under a non-vm parent (RCA #12 — the deploy owns
// its OWN domain, keyed off the full dotted path, matching every other vm-state write's canonical
// spec.VmDomainIdentity(name) scheme), and a dotted path whose ROOT segment is a target:vm deployment (the
// parent deploy owns the domain — the delegate-into-guest shape, e.g. check-arch-vm.arch-host, where
// the dotted suffix addresses something nested INSIDE that vm's guest, not another vm). Leaf checked
// first: a leaf that is itself a vm is never a "delegate into it" address. Mirrors
// candy/plugin-check/venue.go's own plugin-side checkVmTarget (R3 — one classifier PER SIDE of the
// kernel/plugin boundary, no per-call-site re-derivation within either).
func checkVmTarget(uf *loaderkit.UnifiedFile, name string) (domainID string, ok bool) {
	if uf == nil {
		return "", false
	}
	if idx := strings.Index(name, "."); idx > 0 {
		if _, venue, ok := resolveLeafVenue(uf, name); ok && venue == "ssh" { // vm (ssh venue)
			return spec.VmDomainIdentity(name), true
		}
		root := name[:idx]
		if entry, present := uf.Bundle[root]; present && nodeTraits(&entry).Venue == "ssh" { // vm (ssh venue)
			return spec.VmDomainIdentity(root), true
		}
		return "", false
	}
	if uf.VM() != nil {
		if _, present := uf.VM()[name]; present {
			return spec.VmDomainIdentity(name), true
		}
	}
	if uf.Bundle != nil {
		if entry, present := uf.Bundle[name]; present && nodeTraits(&entry).Venue == "ssh" { // vm (ssh venue)
			return spec.VmDomainIdentity(name), true
		}
	}
	return "", false
}

// checkLocalTarget reports whether `name` (or its dotted LEAF, or its dotted root
// segment) is a HOST-VENUE deployment — a `target: local` filesystem apply OR an
// EXTERNAL out-of-process deploy (whose pluginDeployTarget adapter runs its deploy-scope
// probes host-side via ShellExecutor, exactly like local) — and returns its node so
// the caller can build the host/ssh executor via rootExecutorForDeployNode. Mirrors
// candy/plugin-check/venue.go's own plugin-side checkLocalTarget (R3): one classifier PER SIDE
// routes an external deploy to the host path for the interactive `charly check <verb>` (here) and
// the declarative `charly check live` (plugin-side), instead of the pod/container path (which
// would fail at resolveContainer with "container ... is not running").
func checkLocalTarget(uf *loaderkit.UnifiedFile, name string) (spec.BundleNode, bool) {
	if uf == nil || uf.Bundle == nil {
		return spec.BundleNode{}, false
	}
	// A HOST-VENUE substrate runs its deploy-scope check probes on the host — a SHELL venue
	// (local's own filesystem apply; k8s's host-side kubectl), a PARENT venue (android via
	// the parent pod's published adb port), or the NONE / external-in-place venue (an EXTERNAL
	// deploy-class substrate whose pluginDeployTarget adapter applies + probes host-side via
	// ShellExecutor, exactly like local). It is identified by the stamped venue TRAIT (P9),
	// never the kind word. The CONTAINER venue (pod) is NOT host-routed — its check venue is the
	// running container (published ports); a cdp/vnc/spice endpoint or a command/file probe
	// against a pod must resolve the container venue (the default path in resolveCheckVenue),
	// never the host venue (which would dial the raw container port on host loopback). The SSH
	// venue (vm) is NOT host-routed either — it is handled earlier by checkVmTarget (the guest
	// SSHExecutor). (Masked while pod beds used fixed ports H:C==9222:9222; surfaced once they
	// moved to auto-allocated host ports.)
	//
	// RCA #12 (FINAL/K5 unit 6a): checked ONLY the dotted root segment's venue,
	// the same defect class as checkVmTarget — a host-venue child nested (Children,
	// not Members) under a non-host-venue parent was invisible. Leaf checked first,
	// same shared resolveLeafVenue walk; root fallback preserves any existing
	// delegate-into-guest-shaped dotted addressing for a host-venue root.
	if leaf, venue, ok := resolveLeafVenue(uf, name); ok {
		if venue == "shell" || venue == "parent" || venue == "none" {
			return leaf, true
		}
	}
	root := name
	if idx := strings.Index(name, "."); idx > 0 {
		root = name[:idx]
	}
	if entry, present := uf.Bundle[root]; present {
		if v := nodeTraits(&entry).Venue; v == "shell" || v == "parent" || v == "none" {
			return entry, true
		}
	}
	return spec.BundleNode{}, false
}
