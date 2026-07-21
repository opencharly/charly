package substratekind

// status_vm.go — the VM substrate's OpStatus (K5: relocated from
// charly/status_collect_vm.go). Lists charly-* LIBVIRT domains by reaching the
// verb:libvirt plugin (candy/plugin-vm) over the host's InvokeProvider reverse
// leg — the SAME peer-plugin-dispatch seam candy/plugin-example-dispatch
// demonstrates — and maps each domain to a bare DeploymentStatus row. The
// charly.yml deploy-tree ENRICHMENT (SSH-port/network from the matching
// target:vm entry's vm_state) is a SEPARATE concern (mirrors the pod
// substrate's own split: this file returns LIVE rows only; status_flat.go's
// flatCollector.enrichVmRow, K6, same package, applies the deploy-cone
// enrichment afterward) — so this file carries NO BundleConfig/UnifiedFile
// dependency at all.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// vmPluginEnv mirrors charly/vm_plugin_client.go's internal VM-op envelope —
// the wire shape the verb:libvirt plugin's OpRun expects in its Env. Only the
// list-domains fields are populated here.
type vmPluginEnv struct {
	VmOp   string `json:"vm_op"`
	VmName string `json:"vm_name"`
	URI    string `json:"uri"`
}

// domainInfo mirrors charly/vm_plugin_client.go's domainInfo — deliberately NO
// json tags (the wire reply is capitalized Name/State), matching the existing
// verb:libvirt reply shape byte-for-byte.
type domainInfo struct {
	Name  string
	State string
}

// libvirtSessionAvailable reports whether a libvirt session daemon is
// reachable on this host WITHOUT spinning one up (stat-only). A package var
// (mirrors listLibvirtCharlyDomains) so a test can force it available without
// a real session socket.
var libvirtSessionAvailable = defaultLibvirtSessionAvailable

func defaultLibvirtSessionAvailable() bool {
	sock := vmshared.LibvirtSessionSocket()
	if sock == "" {
		return false
	}
	_, err := os.Stat(sock)
	return err == nil
}

// collectVmStatus serves the vm substrate's OpStatusCollect. A libvirt session
// daemon absence, or a verb:libvirt InvokeProvider failure, degrades
// gracefully to zero rows (never an error) — mirrors every other substrate
// collector's graceful-degradation contract.
func collectVmStatus(ctx context.Context, req spec.SubstrateStatusRequest) (spec.SubstrateStatusReply, error) {
	if !libvirtSessionAvailable() {
		return spec.SubstrateStatusReply{}, nil
	}

	domains, err := listLibvirtCharlyDomains(ctx)
	if err != nil || len(domains) == 0 {
		return spec.SubstrateStatusReply{}, nil
	}

	rows := make([]spec.DeploymentStatus, 0, len(domains))
	for _, d := range domains {
		entity := strings.TrimPrefix(d.Name, "charly-")
		rows = append(rows, spec.DeploymentStatus{
			Kind:      spec.SubstrateVM,
			Source:    "libvirt",
			Image:     entity,
			Status:    vmStatusFromDomainState(d.State),
			Container: d.Name,
			RunMode:   req.RunMode,
		})
	}
	return spec.SubstrateStatusReply{Rows: rows}, nil
}

// listLibvirtCharlyDomains reaches the verb:libvirt plugin's list-domains op
// over the host's InvokeProvider reverse leg (the peer-plugin-dispatch F10
// seam) — the SAME wire shape charly/vm_plugin_client.go's invokeVmPlugin used
// host-side, now reached plugin-to-plugin. A package var (mirrors the
// checkvars.go InspectContainer swap pattern) so a test can mock the
// libvirt listing without a live session daemon or a real reverse channel.
var listLibvirtCharlyDomains = defaultListLibvirtCharlyDomains

func defaultListLibvirtCharlyDomains(ctx context.Context) ([]domainInfo, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("reach host reverse channel: %w", err)
	}
	envJSON, err := json.Marshal(vmPluginEnv{VmOp: "list-domains"})
	if err != nil {
		return nil, fmt.Errorf("marshal list-domains request: %w", err)
	}
	out, err := exec.InvokeProvider(ctx, "verb", "libvirt", sdk.OpRun, nil, envJSON, sdk.InvokeProviderOpts{})
	if err != nil {
		return nil, nil // plugin absent/unreachable → no libvirt-backed VMs surface (graceful degrade)
	}
	var doms []domainInfo
	if len(out) > 0 {
		if err := json.Unmarshal(out, &doms); err != nil {
			return nil, fmt.Errorf("decode list-domains reply: %w", err)
		}
	}
	return doms, nil
}

// vmStatusFromDomainState normalises libvirt domain-state vocabulary to the
// unified `charly status` status vocabulary shared with the pod substrate.
// running/paused pass through; every powered-off / transitional libvirt state
// collapses to "stopped" or its closest unified equivalent.
func vmStatusFromDomainState(state string) string {
	switch state {
	case "running":
		return "running"
	case "paused", "suspended":
		return "paused"
	case "crashed":
		return "dead"
	case "shut off", "shutting down", "":
		return "stopped"
	default:
		return "stopped"
	}
}
