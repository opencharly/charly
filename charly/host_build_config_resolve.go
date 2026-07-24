package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// host_build_config_resolve.go — the generic "config-resolve" F10 host-builder. A COMPILED-IN
// command plugin (candy/plugin-vm's command:vm leg) owns its CLI handlers but cannot LoadUnified —
// the config loader + runtime-settings store + backend probe are core Mechanisms (P2), and a plugin
// imports only the sdk. So the host resolves the project config for one entity ONCE and ships it
// back over the reverse channel, exactly as the "deploy-entity-resolve" seam ships a resolved
// kind:vm entity to candy/plugin-deploy-vm's own OpPrepareVenue (FINAL/K5 unit 6a, M4b — the
// deploy-time vmLifecyclePrepare host seam this comment used to reference is deleted; the plugin
// self-serves via that seam instead). The action noun is CLASS-GENERIC
// ("config-resolve"), never a substrate word (the F11 uniform-API gate forbids a provider word on
// the host-builder surface) — the first consumer is command:vm, and the pod (P11) + bundle (P13)
// command families reuse the SAME seam, extending the reply with their own resolved fields.
//
// It returns RESOLVED CONFIG DATA only (the LoadUnified/ResolveRuntime/resolveVmBackend outputs the
// plugin cannot compute host-side); the plugin owns every downstream ACTION (the create pipeline,
// the preempt-lease acquire, the libvirt engine calls). Backend resolution stays here because it is
// a host-ENVIRONMENT probe (is the libvirt session socket up, is qemu installed) — the hostprobe
// category — plus it needs vmConfiguredBackend's LoadUnified pin read.
const configResolveBuilderKind = "config-resolve"

func hostBuildConfigResolve(_ context.Context, req spec.ConfigResolveRequest, _ buildEngineContext) (spec.ConfigResolveReply, error) {
	dir := req.Dir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}

	rt, err := kit.ResolveRuntime()
	if err != nil {
		return spec.ConfigResolveReply{}, err
	}
	reply := spec.ConfigResolveReply{
		VmBackend:   rt.VmBackend,
		BuildEngine: rt.BuildEngine,
		RunEngine:   rt.RunEngine,
	}

	// The kind:vm entity + resources (uf.VM[entity] via the substrate-template resolver + the
	// resource de-type). Graceful-degrade when there is no project (a project-less `charly vm …`):
	// the reply carries only the runtime settings + the backend probe, matching the former in-core
	// handler's `if uf, ok := LoadUnified(dir); ok` branch. VM + Resources are hand-written runtime
	// types with no CUE def, so they travel as opaque JSON envelopes (VmJSON/ResourcesJSON) the plugin
	// decodes; they are resolved into locals here so applyCueDefaults runs on the typed value first.
	var vm *VmSpec
	var resources map[string]*ResolvedResource
	var claimant string
	var claimantNode spec.BundleNode
	var hasClaimant bool
	if uf, ok, ufErr := LoadUnified(dir); ufErr == nil && ok && uf != nil {
		if uf.VM != nil {
			vm, _ = resolveVmViaPlugin(uf.VM[req.Entity])
			for name := range uf.VM {
				reply.VmEntities = append(reply.VmEntities, name)
			}
		}
		resources = uf.resolveResources()
		// The exclusive-resource claimant (requires_exclusive) the handler acquires a preempt
		// lease for — K1-unblock wave 1: the portable deploykit.FindVMClaimant (also used
		// plugin-side by candy/plugin-preempt off the resolved-project envelope, R3) replaces the
		// former core-only lookupVMClaimant, over the SAME per-host-overlay-merged tree.
		claimant, claimantNode, hasClaimant = deploykit.FindVMClaimant(
			deploykit.MergedDeployTree(uf.Bundle, "vm config-resolve"), req.Entity)
	}

	// Effective backend: the entity's `backend:` pin (vmConfiguredBackend) resolved against the live
	// host (resolveVmBackend — which also spawns the libvirt user session before probing the socket).
	backend, err := resolveVmBackend(vmConfiguredBackend(req.Entity, rt.VmBackend))
	if err != nil {
		return spec.ConfigResolveReply{}, err
	}
	reply.Backend = backend

	if hasClaimant {
		reply.Claimant = claimant
		reply.ClaimantNode = &claimantNode
	}

	// Materialize #Vm's required-with-default fields (firmware/network-mode/cpu-mode) on the resolved
	// spec so the plugin's create pipeline receives a fully-defaulted VmSpec (it has no #Vm schema).
	// This supplies the defaults the vm create pipeline (now in candy/plugin-vm) formerly applied
	// in-handler via applyCueDefaults. Order-independent vs
	// the plugin's instance-override / GPU-alloc merge: those touch ONLY libvirt: overlays, never a
	// defaulted field, and applyCueDefaults fills only unset fields (user values preserved by unify).
	//
	// R1 fix (found while verifying an unrelated K5-A cutover — every `charly vm create`/`vm build`
	// was hard-failing): resolveVmViaPlugin's *VmSpec carries the substrate-template opaque echo
	// (ResolvedVm.Raw, the SAME "raw:" passthrough ResolvedK8s/ResolvedLocal also carry) — but #vm's
	// CUE schema is CLOSED over the AUTHORED shape and declares no `raw:` field, so re-marshaling the
	// whole struct here for the unify-with-defaults round-trip failed unify with "raw: field not
	// allowed" on EVERY vm entity. applyCueDefaults' contract is schema-declared-field defaulting
	// only, so the opaque echo is cleared for the round-trip and restored on the vm value the plugin
	// actually receives (Raw is unrelated to firmware/network-mode/cpu-mode defaulting).
	if vm != nil {
		savedRaw := vm.Raw
		vm.Raw = nil
		err := applyCueDefaults("vm", vm)
		vm.Raw = savedRaw
		if err != nil {
			return spec.ConfigResolveReply{}, fmt.Errorf("applying vm defaults for %q: %w", req.Entity, err)
		}
	}

	// Marshal the opaque envelopes AFTER defaulting: VM/Resources are hand-written runtime types with
	// no CUE def (the SDD opaque-bytes carrier), so the CUE-sourced reply ships them as JSON the plugin
	// unmarshals back into *VmSpec / map[string]*ResolvedResource at the boundary.
	if vm != nil {
		b, err := json.Marshal(vm)
		if err != nil {
			return spec.ConfigResolveReply{}, fmt.Errorf("config-resolve: marshal vm for %q: %w", req.Entity, err)
		}
		reply.VmJSON = b
	}
	if resources != nil {
		b, err := json.Marshal(resources)
		if err != nil {
			return spec.ConfigResolveReply{}, fmt.Errorf("config-resolve: marshal resources for %q: %w", req.Entity, err)
		}
		reply.ResourcesJSON = b
	}

	// The persisted deploy-ledger runtime state (READ half): the plugin's build reuses the persisted
	// ssh_port and its create regenerates the seed ISO from this prior state (idempotent auto-port).
	if entry, ok := deploykit.LoadDeployConfigForRead("config-resolve").LookupKey("vm:" + req.Entity); ok {
		reply.VmState = entry.VmState
	}

	return reply, nil
}

// hostBuildConfigPersist is the WRITE twin of hostBuildConfigResolve: the host applies a command
// plugin's deploy-ledger persist/remove under the blocking acquireDeployConfigLock (a core Mechanism
// the plugin cannot hold across the module boundary — a process-shared flock must stay host-side).
// Remove deletes the entry (vm destroy); else the entity's VmState is saved (create persist-auto-port).
// Generic action noun "config-persist" (F11 — never a substrate word); P11/P13 reuse it for their state.
const configPersistBuilderKind = "config-persist"

func hostBuildConfigPersist(_ context.Context, req spec.ConfigPersistRequest, _ buildEngineContext) (spec.ConfigPersistReply, error) {
	if req.Key == "" {
		return spec.ConfigPersistReply{}, fmt.Errorf("config-persist: empty deploy key")
	}
	if req.Remove {
		return spec.ConfigPersistReply{}, removeVmDeployEntry(req.Key)
	}
	return spec.ConfigPersistReply{}, saveVmDeployState(req.Key, req.Entity, req.VmState)
}

var _ = func() bool {
	registerHostBuilder(configResolveBuilderKind, typedHostBuilder(configResolveBuilderKind, hostBuildConfigResolve))
	registerHostBuilder(configPersistBuilderKind, typedHostBuilder(configPersistBuilderKind, hostBuildConfigPersist))
	return true
}()
