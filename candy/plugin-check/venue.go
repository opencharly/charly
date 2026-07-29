package check

// venue.go — K1-unblock W3 Unit A: the venue classifier + executor builder relocated from
// charly/check_venue.go. Every dependency this file had on core-only state (LoadUnified,
// resolveTreeRoot, the core-private providerRegistry via nodeTraits' synthetic-node fallback) is
// replaced by the resolved-project envelope (HostBuild("resolved-project")) already fetched by
// resolvedProject (checkproject.go) — a loader-stamped node ALWAYS carries a non-nil .Descent (via
// the host's stampBundleDescents pass), so the plugin-side nodeTraits below never needs the
// registry-backed synthetic-node fallback the core version carries for un-stamped nodes built
// outside the loader (this package never builds one). Everything else — the poll/readiness/SSH
// forwarding machinery, the container/VM/host classification, the dotted-path tree walk — was
// ALREADY portable via existing sdk/vmshared + sdk/kit primitives; the mechanical rename
// loadedReadiness()→kit.ReadinessProvider() / pollUntil→vmshared.PollUntil /
// ErrPollFatal→vmshared.ErrPollFatal / PollLocal→vmshared.PollLocal / vmDomainIdentity→
// vmshared.VmDomainIdentity is the WHOLE of the "no new mechanism" claim for this file (RDD-
// confirmed by reading sdk/vmshared's own alias/re-export tables before this move).
//
// This file is a self-contained LIBRARY of venue-resolution building blocks, consumed by every
// live-check gather orchestration function this package now carries directly: live_gather.go's
// pluginCheckRunLive/pluginCheckLivePod/pluginCheckLiveVM/pluginCheckLiveLocal/pluginCheckLiveGroup
// (K1-unblock wave arm 1, the plugin-side ports of the former charly/check_cmd.go's
// checkLiveGather/checkLivePod/checkLiveVM/checkLiveLocal/checkLiveGroup) and score_live.go's
// pluginRunCheckLive family (arm 3). Unit B's InvokeProvider-backed newPluginCheckRunner (the
// "check-run" verb-dispatch mechanism) is what made every one of those wirings possible.

import (
	"context"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// resolveCheckEndpoint returns a host-reachable address for the given in-venue TCP port. The
// caller MUST Close() the returned endpoint when done. The former CheckEndpoint type + the
// venue.Kind switch + containerPublishedAddr / sshForwardEndpoint / isHostNetworked bodies
// relocated to sdk/kit (kit.CheckEndpoint + kit.EndpointForVenue) — the ONE kind-blind resolver
// dispatched by the venue's generic transport descriptor, shared with charly core (R3). The
// plugin stamps CheckVenue.Descriptor at construction, exactly like the host side.
func resolveCheckEndpoint(venue *CheckVenue, port int) (*kit.CheckEndpoint, error) {
	return kit.EndpointForVenue(venue.Descriptor, port)
}

// CheckVenue carries a resolved execution venue for an `charly check` verb target.
type CheckVenue struct {
	Exec     deploykit.DeployExecutor
	Kind     string
	Engine   string
	Name     string
	Instance string
	VMName   string
	// Descriptor is the venue's generic transport descriptor (container/ssh/shell), stamped at
	// construction and consumed by the kind-blind kit.EndpointForVenue (R3, shared with core).
	Descriptor spec.VenueDescriptor
}

// IsContainer reports whether the venue is a running container.
func (v *CheckVenue) IsContainer() bool { return v != nil && v.Kind == "container" }

// resolveCheckVenue maps an `charly check` verb's <name> argument to an execution venue, off the
// resolved-project envelope's Deploy tree (rp.Deploy) instead of a direct LoadUnified/
// resolveTreeRoot call — the ONLY change from the core original, which read the SAME merged
// bundle tree via the host loader in-process.
func resolveCheckVenue(ex *sdk.Executor, ctx context.Context, dir, name, instance string) (*CheckVenue, error) {
	if name == "." {
		return &CheckVenue{Exec: kit.ShellExecutor{}, Kind: "host", Descriptor: spec.VenueDescriptor{Kind: "shell"}}, nil
	}

	rp, err := resolvedProject(ex, ctx, dir)
	if err == nil && rp != nil {
		tree := derefDeployTree(rp.Deploy)
		if domainID, isVM := checkVmTarget(tree, name); isVM {
			var vexec deploykit.DeployExecutor = &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 10}
			if strings.Contains(name, ".") {
				if _, chain, chainErr := deploykit.ResolveDeployChain(tree, name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
					vexec = chain
				}
			}
			return &CheckVenue{Exec: vexec, Kind: "vm", Name: domainID, VMName: domainID, Instance: instance,
				Descriptor: spec.VenueDescriptor{Kind: "ssh", Host: kit.VmSshAlias(domainID), ConnectTimeout: 10}}, nil
		}
		if node, isLocal := checkLocalTarget(tree, name); isLocal {
			vexec, lerr := deploykit.RootExecutorForDeployNode(&node)
			if lerr != nil {
				return nil, lerr
			}
			return &CheckVenue{Exec: vexec, Kind: "host", Name: name, Instance: instance,
				Descriptor: kit.DescriptorFromExecutor(vexec)}, nil
		}
		if strings.Contains(name, ".") {
			if _, chain, chainErr := deploykit.ResolveDeployChain(tree, name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
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

	engine, containerName, cerr := deploykit.ResolveContainer(name, instance)
	if cerr != nil {
		return nil, cerr
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

// derefDeployTree converts the envelope's map[string]*spec.BundleNode into the value-map shape
// the tree-walk helpers below (ported unchanged from charly/check_cmd.go + check_venue.go) share
// with every other resolved-project consumer in this package.
func derefDeployTree(m map[string]*spec.BundleNode) map[string]spec.BundleNode {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]spec.BundleNode, len(m))
	for k, v := range m {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

// nodeTraits returns the node's stamped deploy-descent descriptor. Every node reachable off the
// resolved-project envelope is loader-stamped (stampBundleDescents runs host-side before the
// envelope is filled), so — unlike the core original — this plugin-side version never needs the
// registry-backed synthetic-node fallback: this package never constructs a BundleNode outside the
// envelope.
func nodeTraits(node *spec.BundleNode) *spec.DescentDescriptor {
	if node != nil && node.Descent != nil {
		return node.Descent
	}
	return &spec.DescentDescriptor{}
}

// resolveLeafVenue walks a (possibly dotted) name to its LEAF node and reports the LEAF's own
// venue trait.
func resolveLeafVenue(tree map[string]spec.BundleNode, name string) (node spec.BundleNode, venue string, ok bool) {
	if len(tree) == 0 || !strings.Contains(name, ".") {
		return spec.BundleNode{}, "", false
	}
	n, found := resolveDeployNodeByPath(tree, name)
	if !found || n == nil {
		return spec.BundleNode{}, "", false
	}
	return *n, nodeTraits(n).Venue, true
}

// checkVmTarget reports whether `name` resolves to a VM venue and, if so, the per-deploy domain
// identity to SSH into.
func checkVmTarget(tree map[string]spec.BundleNode, name string) (domainID string, ok bool) {
	if idx := strings.Index(name, "."); idx > 0 {
		if _, venue, ok := resolveLeafVenue(tree, name); ok && venue == "ssh" {
			return vmshared.VmDomainIdentity(name), true
		}
		root := name[:idx]
		if entry, present := tree[root]; present && nodeTraits(&entry).Venue == "ssh" {
			return vmshared.VmDomainIdentity(root), true
		}
		return "", false
	}
	if entry, present := tree[name]; present && nodeTraits(&entry).Venue == "ssh" {
		return vmshared.VmDomainIdentity(name), true
	}
	return "", false
}

// checkLocalTarget reports whether `name` (or its dotted LEAF, or its dotted root segment) is a
// HOST-VENUE deployment, returning its node so the caller can build the host/ssh executor via
// deploykit.RootExecutorForDeployNode.
func checkLocalTarget(tree map[string]spec.BundleNode, name string) (spec.BundleNode, bool) {
	if len(tree) == 0 {
		return spec.BundleNode{}, false
	}
	if leaf, venue, ok := resolveLeafVenue(tree, name); ok {
		if venue == "shell" || venue == "parent" || venue == "none" {
			return leaf, true
		}
	}
	root := name
	if idx := strings.Index(name, "."); idx > 0 {
		root = name[:idx]
	}
	if entry, present := tree[root]; present {
		if v := nodeTraits(&entry).Venue; v == "shell" || v == "parent" || v == "none" {
			return entry, true
		}
	}
	return spec.BundleNode{}, false
}

// resolveDeployNodeByPath resolves a (possibly DOTTED) deploy name to its BundleNode, descending
// node.Children for each dotted segment. Ported unchanged from charly/check_cmd.go (pure, no
// core-only dependency).
func resolveDeployNodeByPath(tree map[string]spec.BundleNode, name string) (*spec.BundleNode, bool) {
	name, _ = vmshared.SplitVmAddress(name)
	parts := strings.Split(name, ".")
	root, ok := tree[parts[0]]
	if !ok {
		return nil, false
	}
	cur := &root
	for _, seg := range parts[1:] {
		child, ok := cur.Children[seg]
		if !ok || child == nil {
			return nil, false
		}
		cur = child
	}
	return cur, true
}
