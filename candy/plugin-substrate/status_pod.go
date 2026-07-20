package substratekind

// status_pod.go — the POD substrate's live-collection OpStatus (P14a: the
// LIVE half of charly/status_collect_pod.go + charly/status_collector.go's
// collectOneLive/runProbes/applyQuadletDescription/enabledQuadlets/
// parseQuadletDescription/formatLiveMounts, relocated into the substrate
// plugin). It owns the podman/docker snapshot fan-out + the per-container LIVE
// row (snapshot-derived fields + live mounts + tool probes) — everything
// sdk-only-reachable. The DEPLOY enrichment (charly.yml tunnel + image-label
// fallback) STAYS host-side (deploy-cone-coupled, K5-gated) and is applied to
// the live rows after they cross the OpStatus seam.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/opencharly/sdk/enginekit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// collectPodStatus serves the pod substrate's OpStatusCollect. req.Single
// selects the pod-scoped detail path (one container by box+instance); otherwise
// the full flat fan-out (every charly-* container, plus enabled-but-not-running
// quadlet entries under include_all). Returns LIVE rows only — no deploy
// enrichment (the host applies that).
func collectPodStatus(ctx context.Context, req spec.SubstrateStatusRequest) (spec.SubstrateStatusReply, error) {
	engine := enginekit.NewEngineClient(req.EngineBin)
	if req.Single {
		return collectPodSingle(ctx, engine, req)
	}
	snapshots, err := engine.SnapshotAll(req.IncludeAll)
	if err != nil {
		return spec.SubstrateStatusReply{}, err
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
		applyQuadletDescription(&snapshots[i], req.QuadletDir)
	}

	// include_all in quadlet mode: append enabled-but-not-running entries.
	if req.IncludeAll && req.RunMode == "quadlet" {
		snapshots = append(snapshots, enabledQuadlets(req.QuadletDir, seen)...)
	}

	// Worker pool fan-out across containers.
	results := make([]spec.DeploymentStatus, len(snapshots))
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
			results[i] = collectPodLive(ctx, &snapshots[i], req.RunMode, engine)
		}(i)
	}
	wg.Wait()
	return spec.SubstrateStatusReply{Rows: results}, nil
}

// collectPodSingle is the pod-scoped detail path (charly status <image>). It
// builds a snapshot for the one container (or a synthesized stub when podman
// has no record), applies the quadlet description, and produces the LIVE row.
// The host enriches it + resolves systemd state + secrets after.
func collectPodSingle(ctx context.Context, engine *enginekit.EngineClient, req spec.SubstrateStatusRequest) (spec.SubstrateStatusReply, error) {
	containerName := kit.ContainerNameInstance(req.Box, req.Instance)
	snapshots, _ := engine.SnapshotAll(true)
	var snap *enginekit.ContainerSnapshot
	for i := range snapshots {
		if snapshots[i].Name == containerName {
			snap = &snapshots[i]
			break
		}
	}
	if snap == nil {
		// Not in podman: fall back to a synthesized snapshot; the host's
		// systemd/quadlet resolution distinguishes stopped vs failed vs enabled.
		stub := enginekit.ContainerSnapshot{
			Name:     containerName,
			Box:      req.Box,
			Instance: req.Instance,
		}
		snap = &stub
	} else {
		applyQuadletDescription(snap, req.QuadletDir)
		// applyQuadletDescription may fall back to the joined name; the single
		// path knows the caller-supplied (box, instance) authoritatively.
		snap.Box = req.Box
		snap.Instance = req.Instance
	}
	cs := collectPodLive(ctx, snap, req.RunMode, engine)
	return spec.SubstrateStatusReply{Single: cs}, nil
}

// collectPodLive builds the LIVE pod row: snapshot-derived fields, live mounts,
// and (for a running container) the tool probes. Pure over (snapshot, engine,
// runMode) — NO deploy-config access. This is the relocated charly/Collector
// .collectOneLive; the deploy enrichment (enrichOne) stays host-side.
func collectPodLive(ctx context.Context, snap *enginekit.ContainerSnapshot, runMode string, engine *enginekit.EngineClient) spec.DeploymentStatus {
	cs := spec.DeploymentStatus{
		Kind:      spec.SubstratePod,
		Source:    "podman",
		Image:     snap.Box,
		ImageRef:  snap.ImageRef,
		Instance:  snap.Instance,
		Status:    spec.StatusFromState(snap.State),
		Uptime:    snap.Status,
		Container: snap.Name,
		Devices:   snap.Devices,
		Network:   snap.NetworkMode,
		RunMode:   runMode,
		Ports:     snap.Ports, // RUNTIME truth, always wins for running containers
	}
	if cs.Status == "running" && len(snap.Mounts) > 0 {
		cs.Volumes = formatLiveMounts(snap.Mounts)
	}
	if cs.Status != "running" {
		return cs
	}
	cs.Tools = runPodProbes(ctx, engine, snap)
	return cs
}

// runPodProbes runs all host probes in parallel goroutines and ALL guest probes
// in a single batched podman exec. Per-container subprocess count: ~1 (the
// guest batch) plus N HTTP/TCP probes (host probes don't fork subprocesses).
func runPodProbes(ctx context.Context, engine *enginekit.EngineClient, snap *enginekit.ContainerSnapshot) []spec.ToolStatus {
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
		guestRes = runGuestProbes(ctx, engine, snap.Name, guestProbes)
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
func applyQuadletDescription(snap *enginekit.ContainerSnapshot, quadletDir string) {
	joined := strings.TrimPrefix(snap.Name, "charly-")
	snap.Box = joined
	snap.Instance = ""
	if quadletDir == "" {
		return
	}
	img, inst := parseQuadletDescription(filepath.Join(quadletDir, snap.Name+".container"))
	if img != "" {
		snap.Box = img
		snap.Instance = inst
	}
}

// enabledQuadlets returns synthetic snapshots for quadlet units present on
// disk but not represented in `podman ps -a`. Used by --all to surface
// enabled-but-never-run deployments.
func enabledQuadlets(quadletDir string, seen map[string]bool) []enginekit.ContainerSnapshot {
	if quadletDir == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(quadletDir, "charly-*.container"))
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

// formatLiveMounts renders the live `podman inspect .Mounts[]` view as the
// strings shown in `charly status`'s Volumes column / detail field. For
// type=volume entries, format is `<name>: <mountpoint> -> <dest>`. For
// type=bind, format is `<name-or-bind>: <source> -> <dest>` with an `(enc)`
// suffix when the source path matches the gocryptfs convention
// `<...>/encrypted/<vol>/plain` — the FUSE-mounted plain dir shown to the
// container, NOT the OCI-label default volume name.
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

// isEncryptedPlainPath returns true when path looks like a gocryptfs plain dir
// under a charly-managed encrypted-storage tree, i.e. matches
// `.../encrypted/<anything>/plain`. Used to flag live mounts as encryption FUSE
// mountpoints in the status display. Path-only — does NOT verify the FUSE mount
// is actually live.
func isEncryptedPlainPath(p string) bool {
	if !strings.HasSuffix(p, "/plain") {
		return false
	}
	return strings.Contains(p, "/encrypted/")
}
