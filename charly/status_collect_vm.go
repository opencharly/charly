package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K5 (deploy-cone-coupled;
// see status_substrate.go / CollectOpts for the full rationale, P14-rest trace, 2026-07).
//
// VMCollector is the libvirt SubstrateCollector. It lists charly-* libvirt
// domains via listCharlyDomains(), maps each domain to a DeploymentStatus
// stamped Kind=SubstrateVM, Source="libvirt", and enriches the row from the
// matching target:vm charly.yml entry's vm_state (SSH port/user, backend) when
// one exists. A domain with no deploy entry still shows (Source:libvirt) — the
// libvirt domain list is the source of truth for what is actually defined on
// the host, charly.yml is only enrichment.
//
// Only LIBVIRT-backed domains surface here: listCharlyDomains() queries the
// session daemon, so a VM booted via the qemu backend (pidfile-tracked, not a
// libvirt domain) is not a VMCollector row. That matches `charly vm list`, where
// libvirt domains and qemu pidfiles are two distinct probes.
type VMCollector struct {
	c *Collector
}

// listLibvirtCharlyDomains lists charly-* libvirt domains. Swappable for tests
// (mirrors InspectContainer in checkvars.go) so the table-driven test can mock
// the libvirt listing without a live session daemon. The real implementation
// connects to the local session daemon, lists, and disconnects.
var listLibvirtCharlyDomains = defaultListLibvirtCharlyDomains

func defaultListLibvirtCharlyDomains() ([]domainInfo, error) {
	// List the libvirt domains via the out-of-process vm plugin (the go-libvirt impl moved there).
	raw, ok := invokeVmPlugin("list-domains", "", "")
	if !ok {
		return nil, nil // plugin absent → no libvirt-backed VMs surface (graceful degrade)
	}
	var doms []domainInfo
	if err := json.Unmarshal(raw, &doms); err != nil {
		return nil, err
	}
	return doms, nil
}

func init() {
	registerSubstrate(func(c *Collector) SubstrateCollector { return &VMCollector{c: c} })
}

// Kind reports the vm substrate.
func (v *VMCollector) Kind() spec.SubstrateKind { return spec.SubstrateVM }

// Available reports whether a libvirt session daemon is reachable on this host
// WITHOUT spinning one up. It stat()s the modular/legacy session socket via
// the shared libvirtSessionSocket() probe — the same path resolveVmBackend()
// uses for the "libvirt" backend — and reports reachable only when the socket
// file exists. An absent socket means no libvirt session, so the substrate is
// silently skipped (no rows, no error) per the SubstrateCollector contract.
func (v *VMCollector) Available(opts CollectOpts) bool {
	sock := libvirtSessionSocket()
	if sock == "" {
		return false
	}
	_, err := os.Stat(sock)
	return err == nil
}

// Collect lists charly-* libvirt domains and maps each to a DeploymentStatus.
// Rows are NOT pre-sorted here — Collector.All sorts the merged set across all
// substrates.
func (v *VMCollector) Collect(ctx context.Context, opts CollectOpts) ([]spec.DeploymentStatus, error) {
	domains, err := listLibvirtCharlyDomains()
	if err != nil {
		return nil, err
	}
	rows := make([]spec.DeploymentStatus, 0, len(domains))
	for _, d := range domains {
		rows = append(rows, v.rowForDomain(d, opts))
	}
	return rows, nil
}

// rowForDomain builds a DeploymentStatus for one libvirt domain. The domain name carries the
// canonical charly-<domainIdentity> shape (the domain identity is the DEPLOY name for a bed VM,
// or the entity for a direct `charly vm create`); the charly- prefix is stripped to yield both the
// Image cell and the deploy-first lookup key (findVmDeployNode matches by deploy NAME first, then by
// vm: cross-ref) for vm_state enrichment.
func (v *VMCollector) rowForDomain(d domainInfo, opts CollectOpts) spec.DeploymentStatus {
	entity := strings.TrimPrefix(d.Name, "charly-")
	cs := spec.DeploymentStatus{
		Kind:      spec.SubstrateVM,
		Source:    "libvirt",
		Image:     entity,
		Status:    vmStatusFromDomainState(d.State),
		Container: d.Name,
		RunMode:   opts.RunMode,
	}
	v.enrichFromDeploy(&cs, entity, opts)
	return cs
}

// enrichFromDeploy fills network/backend detail from the matching target:vm
// deploy entry's vm_state when one exists. The deploy entry is resolved via the
// shared findVmDeployNode (deploy-NAME first, then target:vm whose vm: ==
// entity) so a bed whose key differs from its vm entity is still matched.
// Absence of a deploy entry is normal: the libvirt domain still shows with
// Source:libvirt and no enrichment.
func (v *VMCollector) enrichFromDeploy(cs *spec.DeploymentStatus, entity string, opts CollectOpts) {
	if opts.Deploy == nil || opts.Deploy.Bundle == nil {
		return
	}
	node, ok := findVmDeployNode(opts.Deploy.Bundle, entity, entity)
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

// vmStatusFromDomainState normalises libvirt domain-state vocabulary (as
// rendered by domainStateString) to the unified `charly status` status vocabulary
// shared with the pod substrate (spec.StatusFromState, the single-sourced mapper
// in sdk/spec). running/paused pass through; every powered-off / transitional
// libvirt state collapses to a single "stopped" or its closest unified equivalent.
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
