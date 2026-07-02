package main

import "context"

// SubstrateKind identifies which deployment substrate a DeploymentStatus row
// came from. It is the discriminator that lets `charly status` present pod, VM,
// k8s, local, and android deployments side-by-side from one unified table.
type SubstrateKind string

const (
	SubstratePod     SubstrateKind = "pod"
	SubstrateVM      SubstrateKind = "vm"
	SubstrateK8s     SubstrateKind = "k8s"
	SubstrateLocal   SubstrateKind = "local"
	SubstrateAndroid SubstrateKind = "android"
)

// CollectOpts is the read-only input every SubstrateCollector receives. It is
// built once per `charly status` invocation by Collector.All and handed to every
// registered collector unchanged. Nothing in a collector may mutate it.
type CollectOpts struct {
	IncludeAll bool          // mirrors --all
	Nested     bool          // mirrors --nested (live multi-hop probing of nested children + live k8s)
	Deploy     *BundleConfig // ~/.config/charly/charly.yml (may be nil)
	Unified    *UnifiedFile  // charly.yml projection incl. folded kind:check beds (may be nil)
	Engine     *EngineClient // shared podman/docker client
	Quadlet    string        // quadlet dir
	RunMode    string        // c.rt.RunMode
}

// SubstrateCollector is implemented once per deployment substrate. Each
// implementation lives in its OWN file and self-registers via registerSubstrate
// in an init() — there is no central registry slice for downstream stages to
// edit.
type SubstrateCollector interface {
	// Kind reports which substrate this collector covers. Used to stamp
	// DeploymentStatus.Kind and to sort the merged rows.
	Kind() SubstrateKind

	// Available reports whether this substrate's backend is reachable on this
	// host. An unavailable substrate is skipped silently (no error, no rows) —
	// e.g. a libvirt collector on a host with no libvirt session.
	Available(opts CollectOpts) bool

	// Collect gathers status rows for this substrate. A returned error degrades
	// gracefully: Collector.All logs it to stderr and contributes no rows for
	// this kind, but NEVER aborts the whole command.
	Collect(ctx context.Context, opts CollectOpts) ([]DeploymentStatus, error)
}

// collectorFactory builds a SubstrateCollector bound to the active Collector.
// Each substrate file registers exactly one factory.
type collectorFactory func(c *Collector) SubstrateCollector

// substrateFactories is the init()-time registry of every substrate collector.
// Collectors append to it from their own files' init() via registerSubstrate;
// no file edits the slice directly.
var substrateFactories []collectorFactory

// registerSubstrate adds one collector factory to the registry. Called from a
// substrate file's init().
func registerSubstrate(f collectorFactory) {
	substrateFactories = append(substrateFactories, f)
}
