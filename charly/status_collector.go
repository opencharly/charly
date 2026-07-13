package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/opencharly/sdk/enginekit"
	"github.com/opencharly/sdk/spec"
)

// Collector orchestrates one charly-status invocation. Loop-invariant work
// (charly.yml load, quadlet dir lookup, runtime resolution) happens once at
// construction; per-container work runs in a worker pool with a NumCPU*2 cap.
type Collector struct {
	rt      *ResolvedRuntime
	engine  *enginekit.EngineClient
	quadlet string
	deploy  *BundleConfig
	unified *UnifiedFile // best-effort charly.yml projection (may be nil)
}

// NewCollector wires up the runtime + engine + cached deploy + quadlet dir.
// Errors are surfaced for the runtime/engine resolve; charly.yml validation
// failures degrade gracefully (a stderr warning, deploy lookups skipped).
func NewCollector(rt *ResolvedRuntime) (*Collector, error) { //nolint:unparam // error return kept for interface/API stability
	c := &Collector{
		rt:     rt,
		engine: enginekit.NewEngineClient(rt.RunEngine),
	}
	if dc, err := LoadBundleConfig(); err != nil {
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

// collectFlat collects status across every registered deployment substrate
// (pod / vm / k8s / local / android). It builds one read-only CollectOpts, fans
// the available collectors out across a NumCPU*2-bounded goroutine pool,
// merges their rows, and sorts by (Kind, deployKey) — the FLAT fan-out ONLY,
// stopping BEFORE the nested overlay (the overlay is now the command:status
// candy's PURE fold; the host pre-resolves the declared tree separately via
// buildStatusRootsTree). Returns the resolved CollectOpts too, so the caller
// (hostBuildStatusSubstrate) can feed it to buildStatusRootsTree without
// re-deriving c.deploy/c.unified.
//
// A collector returning an error logs a WARNING to stderr and contributes no
// rows (graceful degradation) — it NEVER aborts the whole command. The pod
// substrate's worker-pool fan-out lives inside PodCollector.Collect.
func (c *Collector) collectFlat(ctx context.Context, includeAll, nested bool) ([]spec.DeploymentStatus, CollectOpts, error) { //nolint:unparam // error return kept for interface/API stability
	opts := CollectOpts{
		IncludeAll: includeAll,
		Nested:     nested,
		Deploy:     c.deploy,
		Unified:    c.unified,
		Engine:     c.engine,
		Quadlet:    c.quadlet,
		RunMode:    c.rt.RunMode,
	}

	// Build the available collectors from the init() registry.
	var collectors []SubstrateCollector
	for _, f := range substrateFactories {
		sc := f(c)
		if sc.Available(opts) {
			collectors = append(collectors, sc)
		}
	}

	// Concurrent substrate fan-out, bounded by the same NumCPU*2 cap the pod
	// worker pool uses.
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

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Kind != results[j].Kind {
			return results[i].Kind < results[j].Kind
		}
		return deployKey(results[i].Image, results[i].Instance) < deployKey(results[j].Image, results[j].Instance)
	})
	return results, opts, nil
}

// Single collects status for one image+instance. Sequential — only one
// container, no need for the worker pool. Pod-scoped: the `charly status <image>`
// detail path covers the podman/docker substrate.
func (c *Collector) Single(ctx context.Context, image, instance string) (spec.DeploymentStatus, error) { //nolint:unparam // error return kept for interface/API stability
	boxName := resolveBoxName(image)
	runEngine := ResolveBoxEngineForDeploy(boxName, instance, c.rt.RunEngine)
	engine := enginekit.NewEngineClient(runEngine)
	containerName := containerNameInstance(boxName, instance)

	// Build a snapshot for this single container (bounded engine call surface).
	snapshots, _ := engine.SnapshotAll(true)
	var snap *enginekit.ContainerSnapshot
	for i := range snapshots {
		if snapshots[i].Name == containerName {
			snap = &snapshots[i]
			break
		}
	}
	if snap == nil {
		// Not in podman: fall back to a synthesized snapshot and let the
		// systemd / quadlet path determine its lifecycle state.
		stub := enginekit.ContainerSnapshot{
			Name:     containerName,
			Box:      boxName,
			Instance: instance,
		}
		snap = &stub
	} else {
		c.applyQuadletDescription(snap)
		// applyQuadletDescription may fall back to the joined name if the
		// quadlet description is missing. The single-image path knows the
		// caller-supplied (image, instance) authoritatively, so prefer that.
		snap.Box = boxName
		snap.Instance = instance
	}

	cs := c.collectOne(ctx, snap)
	cs.Secrets = ListProvisionedSecretNames(engine.Bin(), boxName)

	// statusSingle's lifecycle resolution: when the container isn't in
	// podman, consult systemd/quadlet to distinguish stopped vs failed vs
	// enabled vs not configured.
	if cs.Status == "" || cs.Status == "stopped" {
		cs.Status = c.resolveSystemdState(boxName, instance)
	}
	return cs, nil
}

// collectOne builds a DeploymentStatus from a snapshot. Pure function over
// (snapshot, deploy, engine); no global state, safe to call concurrently
// from worker goroutines. Every row is stamped Kind=SubstratePod,
// Source="podman" — this is the pod substrate's row builder.
func (c *Collector) collectOne(ctx context.Context, snap *enginekit.ContainerSnapshot) spec.DeploymentStatus {
	cs := spec.DeploymentStatus{
		Kind:      spec.SubstratePod,
		Source:    "podman",
		Image:     snap.Box,
		ImageRef:  snap.ImageRef,
		Instance:  snap.Instance,
		Status:    statusFromState(snap.State),
		Uptime:    snap.Status,
		Container: snap.Name,
		Devices:   snap.Devices,
		Network:   snap.NetworkMode,
		RunMode:   c.rt.RunMode,
		Ports:     snap.Ports, // RUNTIME truth, always wins for running containers
	}

	// LIVE mounts always win for running containers. snap.Mounts is the
	// `podman inspect .Mounts[]` view — the actual host paths the
	// container is bound to RIGHT NOW. This is the source of truth that
	// distinguishes a `--bind` / `--encrypt` deploy override from the
	// OCI-label default volume backing. Pre-cutover behavior fell through
	// to the charly.yml volume names + image-label fallback — both of
	// which describe what SHOULD be mounted, not what IS, and missed the
	// "FUSE-via-`<cipher>/plain`" case that triggered the immich
	// 2026-04-18 incident's misdiagnosis.
	if cs.Status == "running" && len(snap.Mounts) > 0 {
		cs.Volumes = formatLiveMounts(snap.Mounts)
	}

	// charly.yml enrichment — preferred for tunnel; only fills ports when
	// runtime didn't. Volume fallback only fires when live mounts are
	// unavailable (stopped container).
	if dn, ok := c.lookupDeploy(snap.Box, snap.Instance, snap.Name); ok {
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
	// that had no published ports). Use the BASE image name from the
	// snapshot, not the joined container name.
	if (len(cs.Ports) == 0 || len(cs.Volumes) == 0 || cs.Network == "") && snap.Box != "" {
		ref, _ := ResolveNewestLocalCalVer(c.engine.Bin(), snap.Box)
		if ref != "" {
			if meta, _ := ExtractMetadata(c.engine.Bin(), ref); meta != nil {
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

	if cs.Status != "running" {
		return cs
	}
	cs.Tools = c.runProbes(ctx, snap)
	return cs
}

// runProbes runs all host probes in parallel goroutines and ALL guest probes
// in a single batched podman exec. Per-container subprocess count: ~1 (the
// guest batch) plus N HTTP/TCP probes (host probes don't fork subprocesses).
func (c *Collector) runProbes(ctx context.Context, snap *enginekit.ContainerSnapshot) []spec.ToolStatus {
	var (
		wg       sync.WaitGroup
		hostRes  = make([]spec.ToolStatus, len(hostProbes))
		guestRes []spec.ToolStatus
	)
	for i, p := range hostProbes {
		wg.Add(1)
		go func(i int, p HostProbe) {
			defer wg.Done()
			hostRes[i] = p.ProbeHost(ctx, snap)
		}(i, p)
	}
	wg.Go(func() {
		guestRes = runGuestProbes(ctx, c.engine, snap.Name, guestProbes)
	})
	wg.Wait()

	all := append([]spec.ToolStatus{}, hostRes...)
	all = append(all, guestRes...)
	out := all[:0]
	for _, t := range all {
		if t.Status == "-" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// applyQuadletDescription fills snap.Image and snap.Instance from the
// `Description=OpenCharly <image> (<instance>)` line of the matching quadlet
// unit. Falls through to the joined `charly-*` name when the description isn't
// present (legacy / hand-rolled units).
func (c *Collector) applyQuadletDescription(snap *enginekit.ContainerSnapshot) {
	joined := strings.TrimPrefix(snap.Name, "charly-")
	snap.Box = joined
	snap.Instance = ""
	if c.quadlet == "" {
		return
	}
	img, inst := parseQuadletDescription(filepath.Join(c.quadlet, snap.Name+".container"))
	if img != "" {
		snap.Box = img
		snap.Instance = inst
	}
}

// enabledQuadlets returns synthetic snapshots for quadlet units present on
// disk but not represented in `podman ps -a`. Used by --all to surface
// enabled-but-never-run deployments.
func (c *Collector) enabledQuadlets(seen map[string]bool) []enginekit.ContainerSnapshot {
	if c.quadlet == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(c.quadlet, "charly-*.container"))
	var out []enginekit.ContainerSnapshot
	for _, path := range matches {
		joined := strings.TrimSuffix(filepath.Base(path), ".container")
		if seen[joined] {
			continue
		}
		image, instance := parseQuadletDescription(path)
		if image == "" {
			image = strings.TrimPrefix(joined, "charly-")
		}
		out = append(out, enginekit.ContainerSnapshot{
			Name:     joined,
			State:    "enabled",
			Box:      image,
			Instance: instance,
		})
	}
	return out
}

// lookupDeploy resolves the charly.yml entry for one image+instance. Tries
// the canonical deployKey() shape first, then a few legacy fallbacks for
// bed-rolled keys (joined container name minus charly- prefix).
func (c *Collector) lookupDeploy(box, instance, joinedContainerName string) (BundleNode, bool) {
	if c.deploy == nil || c.deploy.Bundle == nil {
		return BundleNode{}, false
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
	return BundleNode{}, false
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

// statusFromState normalises engine state vocabulary to ours.
func statusFromState(state string) string {
	switch strings.ToLower(state) {
	case "running":
		return "running"
	case "exited", "stopped", "created":
		return "stopped"
	case "dead":
		return "dead"
	case "removing":
		return "removing"
	case "paused":
		return "paused"
	case "enabled":
		return "enabled"
	case "":
		return "stopped"
	default:
		return strings.ToLower(state)
	}
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

// parseQuadletDescription reads a `.container` quadlet file and returns
// (image, instance) parsed from its `Description=OpenCharly <image>
// (<instance>)` line. ("", "") on missing/malformed file — callers fall back
// to the filename-derived joined name.
func parseQuadletDescription(unitPath string) (box, instance string) {
	data, err := os.ReadFile(unitPath)
	if err != nil {
		return "", ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Description=OpenCharly ") {
			continue
		}
		body := strings.TrimPrefix(line, "Description=OpenCharly ")
		if open := strings.LastIndex(body, " ("); open != -1 && strings.HasSuffix(body, ")") {
			box = strings.TrimSpace(body[:open])
			instance = strings.TrimSpace(body[open+2 : len(body)-1])
			return box, instance
		}
		return strings.TrimSpace(body), ""
	}
	return "", ""
}

// formatLiveMounts renders the live `podman inspect .Mounts[]` view as
// the strings shown in `charly status`'s Volumes column / detail field. For
// type=volume entries, format is `<name>: <mountpoint> -> <dest>`. For
// type=bind, format is `<name-or-bind>: <source> -> <dest>` with an
// `(enc)` suffix when the source path matches the gocryptfs convention
// `<...>/encrypted/<vol>/plain` — that's the FUSE-mounted plain dir
// shown to the container, NOT the OCI-label default volume name. The
// (enc) marker is what was missing during the immich-2026-04-18
// diagnosis: `charly status` showed `charly-immich-cache -> /home/user/.immich/
// cache` (the OCI label default) instead of the actual bind to the
// gocryptfs plain dir, masking the encryption state from the operator.
func formatLiveMounts(mounts []enginekit.MountInfo) []string {
	out := make([]string, 0, len(mounts))
	for _, m := range mounts {
		name := m.Name
		if name == "" {
			name = "bind"
		}
		display := fmt.Sprintf("%s: %s -> %s", name, m.Source, m.Destination)
		if isEncryptedPlainPath(m.Source) {
			display += " (enc)"
		}
		out = append(out, display)
	}
	return out
}

// isEncryptedPlainPath returns true when path looks like a gocryptfs
// plain dir under an charly-managed encrypted-storage tree, i.e. matches
// `.../encrypted/<anything>/plain`. Used to flag live mounts as encryption
// FUSE mountpoints in the status display. Path-only — does NOT verify the
// FUSE mount is actually live (that's handled by the verifyBindMounts
// check in /charly-automation:enc).
func isEncryptedPlainPath(p string) bool {
	if !strings.HasSuffix(p, "/plain") {
		return false
	}
	return strings.Contains(p, "/encrypted/")
}

// formatTunnelSummary renders a TunnelYAML as a one-line human-readable
// summary. A COLLECTION helper (DeploymentStatus.Tunnel is already a plain
// string by the time it reaches a renderer), so it lives here — beside its
// sole caller, collectOne — rather than in the command:status candy's pure
// render.go, which formats only already-resolved strings.
func formatTunnelSummary(t *TunnelYAML) string {
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
