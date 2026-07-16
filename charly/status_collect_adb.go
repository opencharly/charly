package main

import (
	"context"
	"sort"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K5 (deploy-cone-coupled;
// see status_substrate.go / CollectOpts for the full rationale, P14-rest trace, 2026-07).
//
// AndroidCollector is the kind:android SubstrateCollector. It enumerates every
// declared `target: android` deploy node (top-level AND nested under a pod) from
// the merged deploy set (charly.yml's folded kind:check beds + ~/.config/charly/
// charly.yml) and resolves each to an AndroidDevice via resolveAndroidDevice. A
// node whose device can't be resolved (emulator pod down, endpoint unreachable) is
// reported as absent rather than aborting the command (graceful degradation — the
// SubstrateCollector contract).
//
// Status is derived host-side WITHOUT goadb (the goadb device-state probe left core
// in the adb → external-plugin dep-shed): an in-pod device is "running" when its pod
// container is up (containerRunning), else "absent"; an endpoint device is "declared"
// (its liveness is only assertable via the `adb:` check verb, which composes the adb
// plugin). The enumeration (which devices exist + the deploy tree) is the
// collector's job; live device state belongs to `charly check adb`.
//
// Every row is stamped Kind=SubstrateAndroid, Source="adb". Container carries
// the device serial (or the undeclared device ref when the spec is missing),
// and the Network cell notes the venue: "in-pod (<container>)" for a box
// device, "endpoint <host:port>" for a remote adb endpoint.
type AndroidCollector struct {
	c *Collector
}

func init() {
	registerSubstrate(func(c *Collector) SubstrateCollector { return &AndroidCollector{c: c} })
}

// Kind reports the android substrate.
func (a *AndroidCollector) Kind() spec.SubstrateKind { return spec.SubstrateAndroid }

// Available reports whether any `target: android` deploy is declared. With no
// android device declared there is nothing to probe and the collector is
// skipped silently.
func (a *AndroidCollector) Available(opts CollectOpts) bool {
	return len(collectAndroidDeployNodes(opts)) > 0
}

// Collect resolves every declared android device and derives its status host-side.
// The work is sequential — there are at most a handful of android devices and each
// status check is a single cheap engine inspect — so no worker pool is warranted.
func (a *AndroidCollector) Collect(ctx context.Context, opts CollectOpts) ([]spec.DeploymentStatus, error) {
	nodes := collectAndroidDeployNodes(opts)
	rows := make([]spec.DeploymentStatus, 0, len(nodes))
	for _, n := range nodes {
		rows = append(rows, a.collectOne(opts, n))
	}
	return rows, nil
}

// androidDeployNode is one declared `target: android` deploy node together with
// the dotted deploy path that addresses it (e.g.
// "check-android-emulator-pod.device") — the path resolveAndroidDevice needs to
// locate the in-pod parent container for a nested device.
type androidDeployNode struct {
	path string
	node spec.BundleNode
}

// collectAndroidDeployNodes is the SINGLE enumeration of every `target: android`
// deploy node, shared by Available and Collect (no duplicated walk). It merges
// the charly.yml projection (incl. folded kind:check beds) with the local
// charly.yml — local wins per key, mirroring resolveTreeRoot's
// MergeDeployConfigs(projectDC, localDC) precedence — then pre-order walks every
// root so nested devices are discovered with their full dotted path.
func collectAndroidDeployNodes(opts CollectOpts) []androidDeployNode {
	merged := deploykit.MergeDeployConfigs(unifiedDeployConfig(opts.Unified), opts.Deploy)
	if merged == nil || merged.Bundle == nil {
		return nil
	}
	var out []androidDeployNode
	for _, name := range sortedDeployKeys(merged.Bundle) {
		root := merged.Bundle[name]
		_ = deploykit.BundleWalkPreOrder(&root, name, func(path string, node *spec.BundleNode) error {
			if node != nil && node.Target == "android" {
				out = append(out, androidDeployNode{path: path, node: *node})
			}
			return nil
		})
	}
	return out
}

// unifiedDeployConfig projects a UnifiedFile to its BundleConfig (folded
// kind:check beds included) or nil when the file is absent.
func unifiedDeployConfig(uf *UnifiedFile) *deploykit.BundleConfig {
	if uf == nil {
		return nil
	}
	return uf.ProjectBundleConfig()
}

// sortedDeployKeys returns the deploy map keys in name order so the android
// table is deterministic across runs.
func sortedDeployKeys(m map[string]spec.BundleNode) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collectOne builds the status row for one declared android device node. It
// resolves the kind:android spec + the device handle, then derives status host-side
// (containerRunning for an in-pod device, "declared" for an endpoint — no goadb).
// Resolution failures degrade to an "absent" row — never an error that would drop
// the whole substrate.
func (a *AndroidCollector) collectOne(opts CollectOpts, dn androidDeployNode) spec.DeploymentStatus {
	row := spec.DeploymentStatus{
		Kind:    spec.SubstrateAndroid,
		Source:  "adb",
		Image:   dn.path,
		Status:  "absent",
		RunMode: opts.RunMode,
	}

	aspec := lookupAndroidSpec(opts.Unified, dn.node.From)
	if aspec == nil {
		// Device reference not declared — surface the deploy path with an
		// absent status so the misconfiguration is visible, not swallowed.
		row.Container = dn.node.From
		return row
	}
	row.Container = aspec.EffectiveSerial()
	if aspec.IsEndpoint() {
		row.Network = "endpoint " + aspec.Adb.Host
	} else if aspec.Box != "" {
		row.Network = "in-pod " + aspec.Box
	}

	dev, err := resolveAndroidDevice(aspec, &dn.node, dn.path)
	if err != nil {
		// Emulator pod not running / endpoint unreachable — absent is the
		// correct, graceful answer.
		return row
	}

	// Derive status host-side, WITHOUT goadb (the device-state probe left core in
	// the adb → external-plugin dep-shed). An in-pod device is "running" when its
	// pod container is up; an endpoint device is "declared" (its live device state
	// is only assertable via the `adb:` check verb, which composes the adb plugin).
	if dev.Engine != "" && dev.Container != "" {
		row.Network = "in-pod (" + dev.Container + ")"
		if containerRunning(dev.Engine, dev.Container) {
			row.Status = "running"
		}
	} else {
		row.Status = "declared"
	}
	return row
}

// lookupAndroidSpec resolves a kind:android device by name from the unified
// config. Returns nil when the file or the device is absent.
func lookupAndroidSpec(uf *UnifiedFile, name string) *ResolvedAndroid {
	if uf == nil || uf.Android == nil || name == "" {
		return nil
	}
	body, ok := uf.Android[name]
	if !ok {
		return nil
	}
	r, err := resolveAndroidViaPlugin(body)
	if err != nil {
		return nil
	}
	return r
}
