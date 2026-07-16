package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// Collector orchestrates one charly-status invocation. Loop-invariant work
// (charly.yml load, quadlet dir lookup, runtime resolution) happens once at
// construction. ALL FIVE substrate collectors (pod/local/vm/k8s/android) now
// live in the substrate plugin (candy/plugin-substrate's OpStatusCollect,
// P14a + K5) and are reached over the kind-provider Invoke — the in-proc
// SubstrateCollector registry this Collector once fanned out to has no
// registrants left and was deleted (status_substrate.go now holds only the
// CollectOpts data-carrier). The Collector itself holds NO enginekit client —
// the live pod collection (podman snapshot + probes) moved to the plugin,
// shedding the enginekit import from this file.
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K5 — the remaining
// orchestration (collectFlat's pod/vm deploy-enrichment calls, Single) is
// deploy-cone-coupled (BundleConfig/UnifiedFile), same as the remaining
// status_nested.go/status_reap.go files (their own switch-on-target-word
// concerns are separate future cutovers — P14-rest trace, 2026-07; see
// status_substrate.go for the CollectOpts rationale).
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
// / local / android) — ALL 5 words now fan out via the substrate plugin's
// OpStatusCollect (P14a + K5; the in-proc SubstrateCollector registry this
// once used for the deploy-cone-coupled substrates has no registrants left
// and was deleted). The plugin returns LIVE rows; the host applies the
// deploy enrichment to the pod + vm rows only (local/k8s/android rows are
// final — each collector's whole collection is deploy-tree-derived inside
// the plugin itself via HostBuild("resolved-project") + (for android)
// deploykit.LoadBundleConfig()). The result is sorted by (Kind, deployKey) —
// the FLAT fan-out ONLY, stopping BEFORE the nested overlay (the overlay is
// the command:status candy's PURE fold; the host pre-resolves the declared
// tree separately via buildStatusRootsTree). Returns the resolved CollectOpts
// too, so the caller (hostBuildStatusSubstrate) can feed it to
// buildStatusRootsTree.
//
// A plugin word returning an error logs a WARNING to stderr and contributes
// no rows (graceful degradation, via collectSubstrate) — it NEVER aborts the
// whole command.
func (c *Collector) collectFlat(ctx context.Context, includeAll, nested bool) ([]spec.DeploymentStatus, CollectOpts, error) { //nolint:unparam // error return kept for interface/API stability
	opts := CollectOpts{
		IncludeAll: includeAll,
		Nested:     nested,
		Deploy:     c.deploy,
		Unified:    c.unified,
		RunMode:    c.rt.RunMode,
	}

	localRows := c.collectSubstrate(ctx, "local", opts)
	k8sRows := c.collectSubstrate(ctx, "k8s", opts)
	androidRows := c.collectSubstrate(ctx, "android", opts)
	vmRows := c.collectSubstrate(ctx, "vm", opts)
	for i := range vmRows {
		c.enrichVmRow(&vmRows[i], opts)
	}
	podRows := c.collectSubstrate(ctx, "pod", opts)
	for i := range podRows {
		c.enrichOne(&podRows[i], c.rt.RunEngine)
	}

	results := make([]spec.DeploymentStatus, 0, len(localRows)+len(k8sRows)+len(androidRows)+len(vmRows)+len(podRows))
	results = append(results, localRows...)
	results = append(results, k8sRows...)
	results = append(results, androidRows...)
	results = append(results, vmRows...)
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
// (pod/local/vm/k8s/android) over the compiled-in kind-provider Invoke. A resolve miss
// or invoke error degrades gracefully: a stderr WARNING + zero rows, never
// aborting the command (mirrors the in-proc collectors' graceful-degradation).
// The ctx carries an in-proc reverse-channel executor (mirrors
// bundle_compile_seam.go's compileViaPlugin) so the vm/k8s arms can reach
// HostBuild("resolved-project") / InvokeProvider("verb","libvirt",...) for
// themselves; pod/local ignore it.
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
	ctx = sdk.ContextWithExecutor(ctx, sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
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
		ref, _ := kit.ResolveNewestLocalCalVer(bin, cs.Image)
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

// enrichVmRow fills network/backend detail from the matching target:vm deploy
// entry's vm_state (~/.config/charly/charly.yml) when one exists (K5:
// relocated from the former VMCollector.enrichFromDeploy). cs.Image already
// carries the libvirt-domain-derived entity name (the substrate plugin
// strips the charly- prefix) — the SAME name deploykit.FindVmDeployNode
// matches by deploy NAME first, then by vm: cross-ref. Absence of a deploy
// entry is normal: the libvirt domain still shows with Source:libvirt and no
// enrichment.
func (c *Collector) enrichVmRow(cs *spec.DeploymentStatus, opts CollectOpts) {
	if opts.Deploy == nil || opts.Deploy.Bundle == nil {
		return
	}
	node, ok := deploykit.FindVmDeployNode(opts.Deploy.Bundle, cs.Image, cs.Image)
	if !ok {
		return
	}
	if node.Network != "" {
		cs.Network = node.Network
	}
	state := node.VmState
	if state == nil {
		return
	}
	// Surface the guest SSH endpoint as a host->guest:22 port mapping so the
	// PORTS column reflects how an operator reaches the VM. This is the live
	// truth recorded by the vm lifecycle hook's PrepareVenue on first apply.
	if state.SshPort > 0 {
		cs.Ports = append(cs.Ports, spec.PortMapping{
			HostPort: state.SshPort,
			CtrPort:  22,
			Proto:    "tcp",
		})
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
