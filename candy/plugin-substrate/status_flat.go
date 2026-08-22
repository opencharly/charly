package substratekind

// status_flat.go — the FLAT status fan-out + deploy-cone enrichment (K6, relocated WHOLE from
// charly/status_collector.go + charly/status_substrate.go). The former "registry-boundary
// blocker" verdict on this file was wrong: the registry access it needed dissolves entirely once
// the fan-out orchestration moves INTO this package — it becomes a direct in-package call to
// statusCollect (status_collect.go, same package) instead of a host-only providerRegistry.resolve
// + wire round-trip. Every other dependency was already sdk-portable, just not yet relocated:
// kit.ExtractMetadata/kit.ResolveBoxName (sdk/kit/box_metadata.go + remote_ref.go, K4 #64),
// deploykit.QuadletDir/QuadletExistsInstance/ServiceNameInstance/ResolveBoxEngineForDeploy
// (sdk/deploykit, K4 #64), kit.ParsePortMapping (already sdk), deploykit.LoadFleetConfig
// (already used elsewhere in this package). ListProvisionedSecretNames is a pure
// exec.Command("podman","secret","ls",...) with zero host-private state — ported directly, no sdk
// dependency needed.
//
// Served by verb:status-fanout's Invoke(sdk.OpStatusCollectAll) (plugin.go), which command:status
// InvokeProvider's DIRECTLY over the in-proc reverse channel — no status-specific business logic
// remains in core (the former host forwarding seam is gone; the broker threads the channel).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// loadFleetConfig reads the per-host deploy overlay (~/.config/charly/charly.yml) via the
// cycle-free plugin-side helper loaderkit.LoadHostFleetConfigViaExecutor (#55 coneC Unit C2 —
// this retired the former deploykit.LoadFleetConfigViaSeam host-handler round-trip;
// loaderkit already imports deploykit so the
// helper lives there and a plugin calls it directly, placement-invariant — the bare
// deploykit.LoadFleetConfig silently no-ops outside charly-core's own init() since
// deploykit.DeployStateHost is only ever registered there; candy/plugin-substrate is compiled-in
// TODAY, so the sibling `deploykit.LoadFleetConfig()` direct calls this replaces were CORRECT
// only by that per-BUILD placement accident — dual-placement is a per-BUILD choice, never an
// authoring guarantee). R3 hoist (charly#176 round 1): the former LoadFleetConfigViaSeam itself
// hoisted four near-identical local copies (candy/plugin-status/nested_tree.go,
// candy/plugin-fleet/ephemeral.go, candy/plugin-pod/remove_orchestration.go, this one); the C2
// helper is now the ONE shared implementation all four call. This package still resolves its OWN
// executor via ctx (sdk.ExecutorForInvoke) before delegating — the multi-call-per-process pattern
// this VERB provider needs (unlike plugin-fleet's COMMAND-plugin package-var, which assumes
// exactly one `charly fleet …` dispatch per process — unsafe to reuse here); HOW a caller
// obtains its executor stays outside the shared helper's concern. Returns (nil, nil) on an
// absent/empty overlay, matching deploykit.LoadFleetConfig's own contract.
func loadFleetConfig(ctx context.Context) (*deploykit.FleetConfig, error) {
	ex, err := sdk.ExecutorForInvoke(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("load fleet config: reach host reverse channel: %w", err)
	}
	return loaderkit.LoadHostFleetConfigViaExecutor(ctx, ex)
}

// runStatusFanout is the sdk.OpStatusCollectAll entry point (plugin.go): req.Single selects the
// pod-scoped detail path (mirrors the former core status command's Collector.Single call);
// otherwise it runs the full multi-substrate fan-out (collectFlat).
func runStatusFanout(ctx context.Context, req spec.StatusSubstrateRequest) (spec.StatusSubstrateReply, error) {
	// ONE resolved-project resolution for the whole fan-out: the kubernetes and android
	// collectors both need the envelope, and resolving it twice is what pushed the check-pod
	// bed's status probe past its 2-minute ceiling under a concurrent roster.
	ctx = withResolvedProjectMemo(ctx)

	rt, err := kit.ResolveRuntime()
	if err != nil {
		return spec.StatusSubstrateReply{}, fmt.Errorf("status-fanout: resolve runtime: %w", err)
	}
	c := newFlatCollector(ctx, rt)

	if req.Single {
		ds, serr := c.collectSingle(ctx, req.Box, req.Instance)
		if serr != nil {
			return spec.StatusSubstrateReply{}, fmt.Errorf("status-fanout: single: %w", serr)
		}
		return spec.StatusSubstrateReply{Single: ds}, nil
	}

	rows, _, ferr := c.collectFlat(ctx, req.IncludeAll)
	if ferr != nil {
		return spec.StatusSubstrateReply{}, fmt.Errorf("status-fanout: collect: %w", ferr)
	}
	return spec.StatusSubstrateReply{Rows: rows}, nil
}

// flatCollector orchestrates one charly-status flat-fanout invocation. Loop-invariant work
// (charly.yml load, quadlet dir lookup, runtime resolution) happens once at construction.
type flatCollector struct {
	rt      *kit.ResolvedRuntime
	quadlet string
	deploy  *deploykit.FleetConfig
}

// flatCollectOpts is the read-only input one collection pass threads through: the deploy-cone
// data enrichOne/enrichVmRow need, plus RunMode for the per-substrate collector requests.
type flatCollectOpts struct {
	IncludeAll bool                   // mirrors --all
	Deploy     *deploykit.FleetConfig // ~/.config/charly/charly.yml (may be nil)
	RunMode    string                 // c.rt.RunMode
}

// newFlatCollector wires up the runtime + cached deploy + quadlet dir. charly.yml validation
// failures degrade gracefully (deploy lookups skipped, no error) — mirrors the former core
// NewCollector exactly (a missing/invalid charly.yml is normal on a fresh host).
func newFlatCollector(ctx context.Context, rt *kit.ResolvedRuntime) *flatCollector {
	c := &flatCollector{rt: rt}
	if dc, err := loadFleetConfig(ctx); err == nil {
		c.deploy = dc
	}
	if qdir, err := deploykit.QuadletDir(); err == nil {
		c.quadlet = qdir
	}
	return c
}

// collectFlat collects status across every deployment substrate (pod/vm/kubernetes/local/android) — ALL
// 5 words fan out via a DIRECT in-package call to statusCollect (status_collect.go, same package)
// — no registry, no wire round-trip for this leg (the fan-out dissolved the former "registry
// blocker": these words are the SAME substrateWords this provider already owns). Applies the
// deploy-cone enrichment to the pod + vm rows only (local/kubernetes/android rows are final — each
// collector's whole collection is deploy-tree-derived inside its own OpStatusCollect handler).
// Sorted by (Kind, deployKey).
func (c *flatCollector) collectFlat(ctx context.Context, includeAll bool) ([]spec.DeploymentStatus, flatCollectOpts, error) {
	opts := flatCollectOpts{
		IncludeAll: includeAll,
		Deploy:     c.deploy,
		RunMode:    c.rt.RunMode,
	}

	localRows := c.collectWord(ctx, "local", opts)
	kubernetesRows := c.collectWord(ctx, "kubernetes", opts)
	androidRows := c.collectWord(ctx, "android", opts)
	vmRows := c.collectWord(ctx, "vm", opts)
	for i := range vmRows {
		c.enrichVmRow(&vmRows[i], opts)
	}
	podRows := c.collectWord(ctx, "pod", opts)
	for i := range podRows {
		c.enrichOne(&podRows[i], c.rt.RunEngine)
	}

	results := make([]spec.DeploymentStatus, 0, len(localRows)+len(kubernetesRows)+len(androidRows)+len(vmRows)+len(podRows))
	results = append(results, localRows...)
	results = append(results, kubernetesRows...)
	results = append(results, androidRows...)
	results = append(results, vmRows...)
	results = append(results, podRows...)

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Kind != results[j].Kind {
			return results[i].Kind < results[j].Kind
		}
		return spec.DeployKey(results[i].Image, results[i].Instance) < spec.DeployKey(results[j].Image, results[j].Instance)
	})
	return results, opts, nil
}

// collectWord calls statusCollect DIRECTLY (in-package Go call — no registry, no wire) for one
// substrate word. A decode/collect error logs a WARNING to stderr and contributes zero rows,
// never aborting the whole command (mirrors the substrate collectors' own graceful degradation).
func (c *flatCollector) collectWord(ctx context.Context, word string, opts flatCollectOpts) []spec.DeploymentStatus {
	req := spec.SubstrateStatusRequest{
		IncludeAll: opts.IncludeAll,
		RunMode:    opts.RunMode,
		QuadletDir: c.quadlet,
		EngineBin:  c.rt.RunEngine,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: charly status: %s collector: marshal request: %v\n", word, err)
		return nil
	}
	res, err := statusCollect(ctx, word, reqJSON)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: charly status: %s collector: %v\n", word, err)
		return nil
	}
	var reply spec.SubstrateStatusReply
	if len(res.json) > 0 {
		if uerr := json.Unmarshal(res.json, &reply); uerr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: charly status: %s collector: decode reply: %v\n", word, uerr)
			return nil
		}
	}
	return reply.Rows
}

// collectSingle collects status for one image+instance (the `charly status <image>` detail path,
// pod-scoped). The LIVE snapshot+row is built by the pod collector (OpStatusCollect single); this
// applies the deploy enrichment + resolves systemd state + lists provisioned secrets.
func (c *flatCollector) collectSingle(ctx context.Context, image, instance string) (spec.DeploymentStatus, error) {
	boxName := kit.ResolveBoxName(image)
	runEngine := deploykit.ResolveBoxEngineForDeploy(boxName, instance, c.rt.RunEngine)

	req := spec.SubstrateStatusRequest{
		Single:     true,
		Box:        boxName,
		Instance:   instance,
		RunMode:    c.rt.RunMode,
		QuadletDir: c.quadlet,
		EngineBin:  runEngine,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.DeploymentStatus{}, fmt.Errorf("status single: marshal request: %w", err)
	}
	res, err := statusCollect(ctx, "pod", reqJSON)
	if err != nil {
		return spec.DeploymentStatus{}, fmt.Errorf("status single: %w", err)
	}
	var reply spec.SubstrateStatusReply
	if len(res.json) > 0 {
		if uerr := json.Unmarshal(res.json, &reply); uerr != nil {
			return spec.DeploymentStatus{}, fmt.Errorf("status single: decode reply: %w", uerr)
		}
	}
	cs := reply.Single
	c.enrichOne(&cs, runEngine)
	cs.Secrets = listProvisionedSecretNames(runEngine, boxName)

	// When the container isn't in podman, consult systemd/quadlet to
	// distinguish stopped vs failed vs enabled vs not configured.
	if cs.Status == "" || cs.Status == "stopped" {
		cs.Status = c.resolveSystemdState(boxName, instance)
	}
	return cs, nil
}

// enrichOne applies the deploy-config + image-label fallbacks to a LIVE pod row (produced by the
// pod collector's OpStatusCollect). It reads ONLY the row (cs.Image/cs.Instance/cs.Container) + a
// binary-name string — NEVER an enginekit snapshot — so it composes with the plugin-served live
// row.
func (c *flatCollector) enrichOne(cs *spec.DeploymentStatus, bin string) {
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
			if meta, _ := kit.ExtractMetadata(bin, ref); meta != nil {
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

// enrichVmRow fills network/backend detail from the matching target:vm deploy entry's vm_state
// (~/.config/charly/charly.yml) when one exists. cs.Image already carries the
// libvirt-domain-derived entity name (the vm collector strips the charly- prefix) — the SAME name
// deploykit.FindVmDeployNode matches by deploy NAME first, then by vm: cross-ref. Absence of a
// deploy entry is normal: the libvirt domain still shows with Source:libvirt and no enrichment.
//
// RCA #14 (FINAL/K5 unit 6a): FindVmDeployNode now reports an AMBIGUOUS
// fallback match (2+ same-base top-level vm deploys) as an error rather than
// first-winning silently. Treated the same as "not found" here — a status
// display row must never error a listing, and this is a best-effort
// enrichment (the same "absence is normal" contract this doc comment already
// states); the row still shows with Source:libvirt, just unenriched.
func (c *flatCollector) enrichVmRow(cs *spec.DeploymentStatus, opts flatCollectOpts) {
	if opts.Deploy == nil || opts.Deploy.Fleet == nil {
		return
	}
	node, ok, err := deploykit.FindVmDeployNode(opts.Deploy.Fleet, cs.Image, cs.Image)
	if !ok || err != nil {
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
	if state.SSHPort > 0 {
		cs.Ports = append(cs.Ports, spec.PortMapping{
			HostPort: state.SSHPort,
			CtrPort:  22,
			Proto:    "tcp",
		})
	}
}

// lookupDeploy resolves the charly.yml entry for one image+instance. Tries the canonical
// deployKey() shape first, then the bed-rolled key shapes (joined container name minus the
// charly- prefix). Those alternates are NOT a legacy charly format — a bed names its own
// containers, so they are shapes charly must still match, not a superseded path to delete.
func (c *flatCollector) lookupDeploy(box, instance, joinedContainerName string) (spec.FleetNode, bool) {
	if c.deploy == nil || c.deploy.Fleet == nil {
		return spec.FleetNode{}, false
	}
	if box != "" {
		if dn, ok := c.deploy.Fleet[spec.DeployKey(box, instance)]; ok {
			return dn, true
		}
		if dn, ok := c.deploy.Fleet[box]; ok && instance == "" {
			return dn, true
		}
	}
	stripped := strings.TrimPrefix(joinedContainerName, "charly-")
	if dn, ok := c.deploy.Fleet[stripped]; ok {
		return dn, true
	}
	return spec.FleetNode{}, false
}

// resolveSystemdState consults systemctl + the quadlet dir to decide whether a non-podman-listed
// deployment is stopped, failed, enabled, or not configured. Used by collectSingle().
func (c *flatCollector) resolveSystemdState(box, instance string) string {
	if c.rt.RunMode != "quadlet" {
		return "stopped"
	}
	svc := deploykit.ServiceNameInstance(box, instance)
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
	exists, _ := deploykit.QuadletExistsInstance(box, instance)
	if exists {
		return "enabled"
	}
	return "not configured"
}

// listProvisionedSecretNames returns the engine-side podman secrets provisioned for a box (the
// charly-<box>-* names, sidecar secrets included), sorted — a pure exec.Command with zero
// host-private state (ported verbatim, no sdk dependency needed).
func listProvisionedSecretNames(engineBin, boxName string) []string {
	out, err := exec.Command(engineBin, "secret", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		return nil
	}
	prefix := "charly-" + boxName + "-"
	var names []string
	for n := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if n != "" && strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// parsePortStrings converts a charly.yml / image-label []string ports list to []PortMapping using
// the canonical kit.ParsePortMapping. Unparseable entries log a WARNING (matches the former
// core behaviour for tunnel ports).
func parsePortStrings(ports []string) []spec.PortMapping {
	if len(ports) == 0 {
		return nil
	}
	var out []spec.PortMapping
	for _, raw := range ports {
		p, ok := kit.ParsePortMapping(strings.TrimSpace(raw))
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

// formatTunnelSummary renders a TunnelYAML as a one-line human-readable summary.
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
