package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// Collector orchestrates one charly-status invocation. Loop-invariant work
// (charly.yml load, quadlet dir lookup, runtime resolution) happens once at
// construction. The pod + local substrate collectors live in the substrate
// plugin (candy/plugin-substrate's OpStatusCollect, P14a) and are reached over
// the kind-provider Invoke; the vm/k8s/android collectors stay host-side
// (deploy-cone-coupled, K5-gated) in the in-proc SubstrateCollector registry.
// The Collector itself holds NO enginekit client — the live pod collection
// (podman snapshot + probes) moved to the plugin, shedding the enginekit
// import from this file.
type Collector struct {
	rt      *ResolvedRuntime
	quadlet string
	deploy  *BundleConfig
	unified *UnifiedFile // best-effort charly.yml projection (may be nil)
}

// NewCollector wires up the runtime + cached deploy + quadlet dir. charly.yml
// validation failures degrade gracefully (a stderr warning, deploy lookups
// skipped).
func NewCollector(rt *ResolvedRuntime) (*Collector, error) { //nolint:unparam // error return kept for interface/API stability
	c := &Collector{rt: rt}
	if dc, err := deploykit.LoadBundleConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: charly.yml has validation errors:\n  %v\n", err)
		fmt.Fprintln(os.Stderr, "(showing image-label-driven results below; resolve the errors to see charly.yml-driven state)")
		fmt.Fprintln(os.Stderr, "")
	} else {
		c.deploy = dc
	}
	if qdir, err := quadletDir(); err == nil {
		c.quadlet = qdir
	}
	// Best-effort charly.yml projection (incl. folded kind:check beds) for
	// the non-pod substrate collectors. Absence / load errors are non-fatal:
	// the unified field stays nil and substrate collectors degrade gracefully.
	if cwd, err := os.Getwd(); err == nil {
		if uf, ok, err := LoadUnified(cwd); err == nil && ok {
			c.unified = uf
		}
	}
	return c, nil
}

// collectFlat collects status across every deployment substrate (pod / vm / k8s
// / local / android). vm/k8s/android fan out via the in-proc SubstrateCollector
// registry (K5-gated, deploy-cone-coupled); pod + local fan out via the
// substrate plugin's OpStatusCollect (P14a), the pod rows then host-enriched.
// The result is sorted by (Kind, deployKey) — the FLAT fan-out ONLY, stopping
// BEFORE the nested overlay (the overlay is the command:status candy's PURE
// fold; the host pre-resolves the declared tree separately via
// buildStatusRootsTree). Returns the resolved CollectOpts too, so the caller
// (hostBuildStatusSubstrate) can feed it to buildStatusRootsTree.
//
// A collector returning an error logs a WARNING to stderr and contributes no
// rows (graceful degradation) — it NEVER aborts the whole command.
func (c *Collector) collectFlat(ctx context.Context, includeAll, nested bool) ([]spec.DeploymentStatus, CollectOpts, error) { //nolint:unparam // error return kept for interface/API stability
	opts := CollectOpts{
		IncludeAll: includeAll,
		Nested:     nested,
		Deploy:     c.deploy,
		Unified:    c.unified,
		RunMode:    c.rt.RunMode,
	}

	// vm/k8s/android via the in-proc SubstrateCollector registry (K5-gated).
	var collectors []SubstrateCollector
	for _, f := range substrateFactories {
		sc := f(c)
		if sc.Available(opts) {
			collectors = append(collectors, sc)
		}
	}
	perKind := make([][]spec.DeploymentStatus, len(collectors))
	workers := max(runtime.NumCPU()*2, 4)
	if workers > len(collectors) {
		workers = len(collectors)
	}
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, sc := range collectors {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, sc SubstrateCollector) {
			defer wg.Done()
			defer func() { <-sem }()
			rows, err := sc.Collect(ctx, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: charly status: %s collector: %v\n", sc.Kind(), err)
				return
			}
			perKind[i] = rows
		}(i, sc)
	}
	wg.Wait()

	var results []spec.DeploymentStatus
	for _, rows := range perKind {
		results = append(results, rows...)
	}

	// pod + local via the substrate plugin's OpStatusCollect (P14a). The plugin
	// returns LIVE rows; the host applies the deploy enrichment to the pod rows
	// (the local rows are final — no deploy enrichment).
	localRows := c.collectSubstrate(ctx, "local", opts)
	results = append(results, localRows...)
	podRows := c.collectSubstrate(ctx, "pod", opts)
	for i := range podRows {
		c.enrichOne(&podRows[i], c.rt.RunEngine)
	}
	results = append(results, podRows...)

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Kind != results[j].Kind {
			return results[i].Kind < results[j].Kind
		}
		return deployKey(results[i].Image, results[i].Instance) < deployKey(results[j].Image, results[j].Instance)
	})
	return results, opts, nil
}

// collectSubstrate reaches the substrate plugin's OpStatusCollect for one word
// (pod/local) over the compiled-in kind-provider Invoke. A resolve miss or
// invoke error degrades gracefully: a stderr WARNING + zero rows, never
// aborting the command (mirrors the in-proc collectors' graceful-degradation).
func (c *Collector) collectSubstrate(ctx context.Context, word string, opts CollectOpts) []spec.DeploymentStatus {
	req := spec.SubstrateStatusRequest{
		IncludeAll: opts.IncludeAll,
		RunMode:    opts.RunMode,
		QuadletDir: c.quadlet,
		EngineBin:  c.rt.RunEngine,
	}
	prov, ok := providerRegistry.resolve(ClassKind, word)
	if !ok {
		fmt.Fprintf(os.Stderr, "WARNING: charly status: %s collector: substrate plugin (kind:%s) not registered\n", word, word)
		return nil
	}
	reply, err := invokeTyped[spec.SubstrateStatusRequest, spec.SubstrateStatusReply](ctx, prov, word, sdk.OpStatusCollect, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: charly status: %s collector: %v\n", word, err)
		return nil
	}
	return reply.Rows
}

// Single collects status for one image+instance (the `charly status <image>`
// detail path, pod-scoped). The LIVE snapshot+row is built in the substrate
// plugin (OpStatusCollect single); the host applies the deploy enrichment +
// resolves systemd state + lists provisioned secrets.
func (c *Collector) Single(ctx context.Context, image, instance string) (spec.DeploymentStatus, error) { //nolint:unparam // error return kept for interface/API stability
	boxName := resolveBoxName(image)
	runEngine := ResolveBoxEngineForDeploy(boxName, instance, c.rt.RunEngine)

	req := spec.SubstrateStatusRequest{
		Single:     true,
		Box:        boxName,
		Instance:   instance,
		RunMode:    c.rt.RunMode,
		QuadletDir: c.quadlet,
		EngineBin:  runEngine,
	}
	prov, ok := providerRegistry.resolve(ClassKind, "pod")
	if !ok {
		return spec.DeploymentStatus{}, fmt.Errorf("substrate plugin (kind:pod) not registered — charly built without the plugin-substrate candy")
	}
	reply, err := invokeTyped[spec.SubstrateStatusRequest, spec.SubstrateStatusReply](ctx, prov, "pod", sdk.OpStatusCollect, req)
	if err != nil {
		return spec.DeploymentStatus{}, fmt.Errorf("status single: %w", err)
	}
	cs := reply.Single
	c.enrichOne(&cs, runEngine)
	cs.Secrets = ListProvisionedSecretNames(runEngine, boxName)

	// When the container isn't in podman, consult systemd/quadlet to
	// distinguish stopped vs failed vs enabled vs not configured.
	if cs.Status == "" || cs.Status == "stopped" {
		cs.Status = c.resolveSystemdState(boxName, instance)
	}
	return cs, nil
}

// enrichOne applies the deploy-config + image-label fallbacks to a LIVE pod row
// (produced by the substrate plugin's OpStatusCollect). It reads ONLY the row
// (cs.Image/cs.Instance/cs.Container) + a binary-name string — NEVER an
// enginekit snapshot — so it composes with the plugin-served live row. Stays
// host-side (deploy-cone-coupled via c.deploy/BundleNode) UNTIL K5.
func (c *Collector) enrichOne(cs *spec.DeploymentStatus, bin string) {
	// charly.yml enrichment — preferred for tunnel; only fills ports when
	// runtime didn't. Volume fallback only fires when live mounts are
	// unavailable (stopped container).
	if dn, ok := c.lookupDeploy(cs.Image, cs.Instance, cs.Container); ok {
		if cs.Tunnel == "" && dn.Tunnel != nil {
			cs.Tunnel = formatTunnelSummary(dn.Tunnel)
		}
		if len(cs.Ports) == 0 {
			cs.Ports = parsePortStrings(dn.Port)
		}
		if cs.Network == "" {
			cs.Network = dn.Network
		}
		if len(cs.Volumes) == 0 {
			for _, v := range dn.Volume {
				cs.Volumes = append(cs.Volumes, v.Name)
			}
		}
	}

	// Image-label fallback for stopped/enabled rows (and any running row
	// that had no published ports). Use the BASE image name from the row,
	// not the joined container name.
	if (len(cs.Ports) == 0 || len(cs.Volumes) == 0 || cs.Network == "") && cs.Image != "" {
		ref, _ := ResolveNewestLocalCalVer(bin, cs.Image)
		if ref != "" {
			if meta, _ := ExtractMetadata(bin, ref); meta != nil {
				if len(cs.Ports) == 0 {
					cs.Ports = parsePortStrings(meta.Port)
				}
				if cs.Network == "" {
					cs.Network = meta.Network
				}
				if len(cs.Volumes) == 0 {
					for _, v := range meta.Volume {
						cs.Volumes = append(cs.Volumes,
							fmt.Sprintf("%s -> %s", v.VolumeName, v.ContainerPath))
					}
				}
			}
		}
	}
}

// lookupDeploy resolves the charly.yml entry for one image+instance. Tries
// the canonical deployKey() shape first, then a few legacy fallbacks for
// bed-rolled keys (joined container name minus charly- prefix).
func (c *Collector) lookupDeploy(box, instance, joinedContainerName string) (spec.BundleNode, bool) {
	if c.deploy == nil || c.deploy.Bundle == nil {
		return spec.BundleNode{}, false
	}
	if box != "" {
		if dn, ok := c.deploy.Bundle[deployKey(box, instance)]; ok {
			return dn, true
		}
		if dn, ok := c.deploy.Bundle[box]; ok && instance == "" {
			return dn, true
		}
	}
	stripped := strings.TrimPrefix(joinedContainerName, "charly-")
	if dn, ok := c.deploy.Bundle[stripped]; ok {
		return dn, true
	}
	return spec.BundleNode{}, false
}

// resolveSystemdState consults systemctl + the quadlet dir to decide whether
// a non-podman-listed deployment is stopped, failed, enabled, or not
// configured. Used by Single().
func (c *Collector) resolveSystemdState(box, instance string) string {
	if c.rt.RunMode != "quadlet" {
		return "stopped"
	}
	svc := serviceNameInstance(box, instance)
	out, err := exec.Command("systemctl", "--user", "is-active", svc).Output()
	if err == nil {
		switch strings.TrimSpace(string(out)) {
		case "active":
			return "running"
		case "failed":
			return "failed"
		default:
			return "stopped"
		}
	}
	exists, _ := quadletExistsInstance(box, instance)
	if exists {
		return "enabled"
	}
	return "not configured"
}

// parsePortStrings converts a charly.yml / image-label []string ports list
// to []PortMapping using the canonical ParsePortMapping. Unparseable entries
// log loudly to stderr (matches the existing behaviour for tunnel ports).
func parsePortStrings(ports []string) []spec.PortMapping {
	if len(ports) == 0 {
		return nil
	}
	var out []spec.PortMapping
	for _, raw := range ports {
		p, ok := ParsePortMapping(strings.TrimSpace(raw))
		if !ok {
			fmt.Fprintf(os.Stderr, "WARNING: charly status: cannot parse port mapping %q\n", raw)
			continue
		}
		out = append(out, spec.PortMapping{
			HostIP:   p.BindAddr,
			HostPort: p.Host,
			CtrPort:  p.Container,
			Proto:    p.Protocol,
		})
	}
	return out
}

// formatTunnelSummary renders a TunnelYAML as a one-line human-readable
// summary. A COLLECTION helper (DeploymentStatus.Tunnel is already a plain
// string by the time it reaches a renderer), so it lives here — beside its
// sole caller, enrichOne — rather than in the command:status candy's pure
// render.go, which formats only already-resolved strings.
func formatTunnelSummary(t *spec.TunnelYAML) string {
	if t == nil {
		return ""
	}
	provider := t.Provider
	if provider == "" {
		provider = "tailscale"
	}
	if t.Public.All || t.Private.All {
		return fmt.Sprintf("%s (all ports)", provider)
	}
	ports := make([]int, 0, len(t.Public.Ports)+len(t.Private.Ports))
	ports = append(ports, t.Public.Ports...)
	ports = append(ports, t.Private.Ports...)
	if len(ports) > 0 {
		ps := make([]string, len(ports))
		for i, p := range ports {
			ps[i] = fmt.Sprintf("%d", p)
		}
		return fmt.Sprintf("%s (ports %s)", provider, strings.Join(ps, ","))
	}
	return provider
}
