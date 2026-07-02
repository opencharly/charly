package main

import (
	"context"
	"runtime"
	"strings"
	"sync"
)

// PodCollector is the pod/container SubstrateCollector. It wraps the existing
// podman/docker collection logic: one batched SnapshotAll, filter to charly-*,
// quadlet-description enrichment, enabled-but-not-running quadlet append (under
// --all), then a NumCPU*2 worker-pool fan-out over collectOne. Every row is
// stamped Kind=SubstratePod, Source="podman". This is the byte-identical
// successor to the pre-substrate Collector.All body.
type PodCollector struct {
	c *Collector
}

func init() {
	registerSubstrate(func(c *Collector) SubstrateCollector { return &PodCollector{c: c} })
}

// Kind reports the pod substrate.
func (p *PodCollector) Kind() SubstrateKind { return SubstratePod }

// Available always returns true — podman/docker is the baseline substrate, and
// an absent engine surfaces as a Collect error (graceful degradation) rather
// than an availability gate.
func (p *PodCollector) Available(opts CollectOpts) bool { return true }

// Collect gathers status for every running charly-* container, plus enabled-but-
// not-running quadlet entries when opts.IncludeAll is set. Per-container work
// is fanned out across a NumCPU*2 worker pool. Rows are NOT pre-sorted here —
// Collector.All sorts the merged set across all substrates.
func (p *PodCollector) Collect(ctx context.Context, opts CollectOpts) ([]DeploymentStatus, error) {
	c := p.c
	snapshots, err := c.engine.SnapshotAll(opts.IncludeAll)
	if err != nil {
		return nil, err
	}
	// Filter to charly-* (the ps filter is name=charly- which already matches, but
	// belt-and-braces in case docker fuzz-matches differently).
	filtered := snapshots[:0]
	seen := map[string]bool{}
	for _, s := range snapshots {
		if !strings.HasPrefix(s.Name, "charly-") {
			continue
		}
		filtered = append(filtered, s)
		seen[s.Name] = true
	}
	snapshots = filtered

	// Quadlet enrichment: split joined container name into image + instance.
	for i := range snapshots {
		c.applyQuadletDescription(&snapshots[i])
	}

	// --all in quadlet mode: append enabled-but-not-running entries.
	if opts.IncludeAll && c.rt.RunMode == "quadlet" {
		snapshots = append(snapshots, c.enabledQuadlets(seen)...)
	}

	// Worker pool fan-out across containers.
	results := make([]DeploymentStatus, len(snapshots))
	workers := max(runtime.NumCPU()*2, 4)
	if workers > len(snapshots) {
		workers = len(snapshots)
	}
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range snapshots {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = c.collectOne(ctx, &snapshots[i])
		}(i)
	}
	wg.Wait()
	return results, nil
}
