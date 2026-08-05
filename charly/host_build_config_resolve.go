package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/spec/hostenv"
	"github.com/opencharly/spec/spec"
)

// host_build_config_resolve.go — the generic "config-resolve" F10 host-builder. A COMPILED-IN
// command plugin (candy/plugin-vm's command:vm leg) owns its CLI handlers but cannot LoadUnified —
// the config loader + runtime-settings store + backend probe are core Mechanisms (P2), and a plugin
// imports only the sdk. So the host resolves the project config for one entity ONCE and ships it
// back over the reverse channel (the deploy-time vmLifecyclePrepare host seam this comment used to
// reference is deleted; candy/plugin-deploy-vm's own OpPrepareVenue self-serves its kind:vm entity
// PLUGIN-SIDE now, via sdk/loaderkit.ResolveVmEntityViaExecutor — K-wave W3a A3-phase-2, the same
// self-load pattern this file's OWN vm.backend consult below also uses). The action noun is
// CLASS-GENERIC ("config-resolve"), never a substrate word (the F11 uniform-API gate forbids a
// provider word on the host-builder surface) — the first consumer is command:vm, and the pod (P11)
// + bundle (P13) command families reuse the SAME seam, extending the reply with their own resolved
// fields.
//
// It returns RESOLVED CONFIG DATA only (the LoadUnified/ResolveRuntime outputs the plugin cannot
// compute host-side); the plugin owns every downstream ACTION (the create pipeline, the
// preempt-lease acquire, the libvirt engine calls). Backend resolution (resolveVmBackend/
// vmConfiguredBackend) moved plugin-side (F6 vm-lifecycle move, coneB-vmlifecycle,
// candy/plugin-vm/vm_backend_resolve.go): it turned out to be a pure host-env probe with zero
// core-registry coupling, and its one LoadUnified-coupled dependency (the entity's `backend:` pin)
// self-loads the project directly now (sdk/loaderkit.ResolveVmEntityViaExecutor, K-wave W3a
// A3-phase-2) — so the reply no longer carries Backend.
const configResolveBuilderKind = "config-resolve"

func hostBuildConfigResolve(_ context.Context, req spec.ConfigResolveRequest, _ buildEngineContext) (spec.ConfigResolveReply, error) {
	dir := req.Dir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}

	rt, err := hostenv.ResolveRuntime()
	if err != nil {
		return spec.ConfigResolveReply{}, err
	}
	reply := spec.ConfigResolveReply{
		VmBackend:   rt.VmBackend,
		BuildEngine: rt.BuildEngine,
		RunEngine:   rt.RunEngine,
	}

	// The kind:vm entity + resources (uf.VM()[entity] via the substrate-template resolver + the
	// resource de-type). Graceful-degrade when there is no project (a project-less `charly vm …`):
	// the reply carries only the runtime settings + the backend probe, matching the former in-core
	// handler's `if uf, ok := LoadUnified(dir); ok` branch. VM + Resources are hand-written runtime
	// types with no CUE def, so they travel as opaque JSON envelopes (VmJSON/ResourcesJSON) the plugin
	// decodes; they are resolved into locals here so ApplyCueDefaults runs on the typed value first.
	var vm *spec.ResolvedVm
	var resources map[string]*spec.ResolvedResource
	if uf, ok, ufErr := LoadUnified(dir); ufErr == nil && ok && uf != nil {
		if uf.VM() != nil {
			vm, _ = resolveVmViaPlugin(uf.VM()[req.Entity])
			for name := range uf.VM() {
				reply.VmEntities = append(reply.VmEntities, name)
			}
		}
		resources = resolveResources(uf)
		// #55 coneC-dsh β2 config-RESOLVE shed: the host no longer calls deploykit.MergedDeployTree +
		// spec.FindVMClaimant (the per-host-overlay-merged Claimant lookup). It now ships the PROJECT
		// deploy tree (uf.Bundle) as BundleJSON; the plugin (candy/plugin-vm hostConfigResolve) merges
		// it with the per-host overlay ITSELF via deploykit.MergedDeployTree(bundle, ctx, reader) +
		// spec.FindVMClaimant (placement-invariant — the reader is loaderkit.LoadHostBundleConfigViaExecutor,
		// so the Claimant computation no longer depends on the deleted host DeployStateHost seam). This
		// drops the last deploykit import from this file.
		if bundleJSON, mErr := json.Marshal(uf.Bundle); mErr == nil {
			reply.BundleJSON = bundleJSON
		}
	}

	// Materialize #Vm's required-with-default fields (firmware/network-mode/cpu-mode) on the resolved
	// spec so the plugin's create pipeline receives a fully-defaulted spec.ResolvedVm (it has no #Vm schema).
	// This supplies the defaults the vm create pipeline (now in candy/plugin-vm) formerly applied
	// in-handler via the loader seam's ApplyCueDefaults. Order-independent vs
	// the plugin's instance-override / GPU-alloc merge: those touch ONLY libvirt: overlays, never a
	// defaulted field, and ApplyCueDefaults fills only unset fields (user values preserved by unify).
	//
	// R1 fix (found while verifying an unrelated K5-A cutover — every `charly vm create`/`vm build`
	// was hard-failing): resolveVmViaPlugin's *spec.ResolvedVm carries the substrate-template opaque echo
	// (ResolvedVm.Raw, the SAME "raw:" passthrough spec.ResolvedK8s/spec.ResolvedLocal also carry) — but #vm's
	// CUE schema is CLOSED over the AUTHORED shape and declares no `raw:` field, so re-marshaling the
	// whole struct here for the unify-with-defaults round-trip failed unify with "raw: field not
	// allowed" on EVERY vm entity. ApplyCueDefaults' contract is schema-declared-field defaulting
	// only, so the opaque echo is cleared for the round-trip and restored on the vm value the plugin
	// actually receives (Raw is unrelated to firmware/network-mode/cpu-mode defaulting).
	if vm != nil {
		savedRaw := vm.Raw
		vm.Raw = nil
		err := requireProjectLoader().ApplyCueDefaults("vm", vm)
		vm.Raw = savedRaw
		if err != nil {
			return spec.ConfigResolveReply{}, fmt.Errorf("applying vm defaults for %q: %w", req.Entity, err)
		}
	}

	// Marshal the opaque envelopes AFTER defaulting: VM/Resources are hand-written runtime types with
	// no CUE def (the SDD opaque-bytes carrier), so the CUE-sourced reply ships them as JSON the plugin
	// unmarshals back into *spec.ResolvedVm / map[string]*ResolvedResource at the boundary.
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
	// #55 coneC-dsh β2 config-RESOLVE (Approach X): the host reads VmState spec-ONLY via a second
	// LoadUnified over the per-host overlay dir — NO deploykit.LoadDeployConfigForRead (which would
	// route through the deleted DeployStateHost seam). LoadUnified(perHostConfigDir).Bundle is
	// byte-equivalent to deploykit.LoadBundleConfig().Bundle (ProjectBundleConfig just extracts
	// uf.Bundle, deploy_file.go:81-110) for the VmState lookup key "vm:"+entity. The host keeps
	// filling VmState because THREE plugins consume it (plugin-vm hostConfigResolve + plugin-kube
	// hostConfigResolveVmState + plugin-deploy-vm lifecycle) — only the Claimant moved plugin-side.
	//
	// R1 documented-divergence: LoadDeployConfigForRead emitted a legacy-`deploy.yml`-filename
	// stderr warning on a host still on the legacy filename (deploy_file.go:92-96); this spec-only
	// LoadUnified path does NOT emit that warning. The migration guard itself is NOT lost — it
	// still fires on every OTHER LoadBundleConfig caller (the β1 plugin-side readers + SaveBundleConfig
	// fail-safe re-reads) which return the error properly. Only this config-resolve VmState READ no
	// longer routes through the deploykit file-shell owning the guard. On a legacy-deploy.yml host
	// both paths produce the SAME load-bearing outcome (nil VmState — the prior LoadDeployConfigForRead
	// already degraded to empty there); only the stderr diagnostic line differs.
	if path, perr := spec.DefaultDeployConfigPath(); perr == nil {
		if ovUf, ok2, _ := LoadUnified(filepath.Dir(path)); ok2 && ovUf != nil {
			if entry, ok := ovUf.Bundle["vm:"+req.Entity]; ok {
				reply.VmState = entry.VmState
			}
		}
	}

	return reply, nil
}

// The "config-persist" host-builder is DELETED (#55 coneC-dsh β2 config-PERSIST shed): the
// command:vm + command:bundle plugins now call deploykit.SaveVmDeployState/RemoveVmDeployEntry
// directly with their own lock + marshal + reader (candy/plugin-vm/vm_host_persist.go +
// candy/plugin-bundle/deploy_target.go), over the existing reader-callback precedent. The
// config-RESOLVE leg is ALSO off deploykit (#55 coneC-dsh β2 config-RESOLVE shed): the host ships
// the PROJECT bundle as BundleJSON (the plugin merges the per-host overlay + computes the Claimant
// itself via deploykit.MergedDeployTree+spec.FindVMClaimant) + reads VmState spec-only via LoadUnified
// of the per-host overlay dir. host_build_config_resolve.go imports ONLY spec/* (+ hostenv) — ZERO
// deploykit.

var _ = func() bool {
	registerHostBuilder(configResolveBuilderKind, typedHostBuilder(configResolveBuilderKind, hostBuildConfigResolve))
	return true
}()
