package vm

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// vm_host_seams.go — the command:vm plugin's bridge to the host. The VM CLI handlers moved out of
// charly core (P10); the config loader + runtime-settings store + deploy ledger + egress subsystem
// are core Mechanisms a plugin cannot import (separate module), so the handlers reach them over the
// in-proc reverse channel: config → HostBuild("config-resolve"), ledger writes plugin-side
// (candy/plugin-vm/vm_host_persist.go — the former HostBuild("config-persist") is DELETED),
// egress → InvokeProvider(verb:egress). command:vm is COMPILED-IN and dispatches
// exactly ONE `charly vm …` invocation per process, so the reverse-channel executor is stashed in a
// package var at Invoke(OpRun) entry (setCommandContext) — race-free single-command-per-process.

// Spec-type aliases the moved handlers reference by their core (package main) short names. All are
// canonical sdk/spec wire types (the same identity core used via its own alias surface).
type (
	FleetNode          = spec.Deploy
	ResolvedResource    = spec.ResolvedResource
	ResolvedGpuSelector = spec.ResolvedGpuSelector
	VFIOReport          = spec.VFIOReport
	VFIOPCIDevice       = spec.VFIOPCIDevice
)

// cmdCtx / cmdExec carry the Invoke(OpRun) reverse-channel handle to the deep CLI call sites.
var (
	cmdCtx  context.Context
	cmdExec *sdk.Executor
)

// setCommandContext stashes the reverse-channel executor for the duration of one `charly vm …`
// dispatch. Called once at the top of command:vm's Invoke(OpRun).
func setCommandContext(ctx context.Context, ex *sdk.Executor) {
	cmdCtx = ctx
	cmdExec = ex
}

// resolvedConfig is the plugin-facing result of hostConfigResolve. Since K-wave 2 cone R2 bank D
// (the "config-resolve" HostBuild seam DELETED), hostConfigResolve computes every field
// PLUGIN-SIDE — the kind:vm entity resolves to *spec.ResolvedVm directly (vmshared.VmSpec IS
// spec.ResolvedVm, so the former VmJSON envelope decode was identity), the resources via
// spec.ResolvePluginKindViaPlugin over loaderkit.ResolveResourceViaExecutor, the runtime settings
// via kit.ResolveRuntime, and VmState via loaderkit.ResolveVmStateViaExecutor.
// Claimant/ClaimantNode are computed PLUGIN-SIDE (deploykit.MergedDeployTree + spec.FindVMClaimant
// over the plugin's loader-backed reader).
type resolvedConfig struct {
	VM           *VmSpec
	Resources    map[string]*ResolvedResource
	Backend      string
	Claimant     string
	ClaimantNode *spec.Deploy
	VmBackend    string
	BuildEngine  string
	RunEngine    string
	VmState      *spec.VmDeployState
	VmEntities   []string
}

// hostConfigResolve resolves the project config for an entity PLUGIN-SIDE (K-wave 2 cone R2 bank D
// — the "config-resolve" HostBuild seam is DELETED): the runtime settings come from
// kit.ResolveRuntime (this plugin is compiled-in, sharing charly's process + runtime config), the
// project loads via loaderkit.LoadUnifiedViaExecutor, the kind:vm entity resolves via
// loaderkit.ResolveVmEntityViaExecutor + loaderkit.ApplyCueDefaults (the schema-declared defaults
// the former host seam applied), the resources via spec.ResolvePluginKindViaPlugin over
// loaderkit.ResolveResourceViaExecutor, and the persisted VmState via
// loaderkit.ResolveVmStateViaExecutor. The exclusive-resource Claimant is computed PLUGIN-SIDE
// (#55 coneC-dsh β2 config-RESOLVE) from the loaded project fleet via deploykit.MergedDeployTree +
// spec.FindVMClaimant; the effective VM backend is computed HERE (resolveVmBackendPlugin/
// vmConfiguredBackendPlugin, F6 vm-lifecycle move, vm_backend_resolve.go).
func hostConfigResolve(entity string) (resolvedConfig, error) {
	if cmdExec == nil {
		return resolvedConfig{}, fmt.Errorf("config-resolve: no host reverse channel (command not compiled-in?)")
	}
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return resolvedConfig{}, err
	}
	dir, _ := os.Getwd()
	cfg := resolvedConfig{
		VmBackend:   rt.VmBackend,
		BuildEngine: rt.BuildEngine,
		RunEngine:   rt.RunEngine,
	}
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(cmdCtx, cmdExec, dir)
	if err != nil {
		return resolvedConfig{}, fmt.Errorf("config-resolve: load project: %w", err)
	}
	if ok && uf != nil {
		// The kind:vm entity names + the requested entity's resolved spec (vmshared.VmSpec IS
		// spec.ResolvedVm, so no wire envelope decode — the former VmJSON round-trip was identity).
		for name := range uf.VM() {
			cfg.VmEntities = append(cfg.VmEntities, name)
		}
		if entity != "" {
			if body, has := uf.VM()[entity]; has && len(body) > 0 {
				vm, verr := loaderkit.ResolveVmEntityViaExecutor(cmdCtx, cmdExec, dir, entity)
				if verr != nil {
					return resolvedConfig{}, verr
				}
				// ApplyCueDefaults fills schema-declared defaults. Order-independent vs the
				// plugin's instance-override / GPU-alloc merge (those touch ONLY libvirt overlays).
				// The opaque substrate-template echo (Raw) is cleared for the closed-schema unify
				// round-trip and restored on the value the plugin receives.
				if vm != nil {
					savedRaw := vm.Raw
					vm.Raw = nil
					if derr := loaderkit.ApplyCueDefaults("vm", vm); derr != nil {
						return resolvedConfig{}, fmt.Errorf("applying vm defaults for %q: %w", entity, derr)
					}
					vm.Raw = savedRaw
					cfg.VM = vm
				}
			}
		}
		cfg.Resources = spec.ResolvePluginKindViaPlugin(uf, "resource", loaderkit.ResolveResourceViaExecutor(cmdCtx, cmdExec))
		// Claimant computation moved plugin-side (#55 coneC-dsh β2 config-RESOLVE): merge the
		// per-host overlay via deploykit.MergedDeployTree (placement-invariant reader =
		// loaderkit.LoadHostFleetConfigViaExecutor) + spec.FindVMClaimant.
		merged := deploykit.MergedDeployTree(uf.Fleet, "vm config-resolve", func() (*deploykit.FleetConfig, error) {
			return loaderkit.LoadHostFleetConfigViaExecutor(cmdCtx, cmdExec)
		})
		if claimant, claimantNode, hasClaimant := spec.FindVMClaimant(merged, entity); hasClaimant {
			cfg.Claimant = claimant
			cfg.ClaimantNode = &claimantNode
		}
	}
	// The persisted deploy-ledger runtime state (READ half) — plugin-side (the former
	// config-resolve VmState leg).
	cfg.VmState, _ = loaderkit.ResolveVmStateViaExecutor(cmdCtx, cmdExec, entity)
	backend, err := resolveVmBackendPlugin(vmConfiguredBackendPlugin(cmdCtx, cmdExec, entity, cfg.VmBackend))
	if err != nil {
		return resolvedConfig{}, err
	}
	cfg.Backend = backend
	return cfg, nil
}

// hostConfigPersist now lives in vm_host_persist.go — the PLUGIN-SIDE deploy-ledger persist path
// (#55 coneC-dsh β2 config-PERSIST shed: the former HostBuild("config-persist") host-builder is
// deleted; the plugin calls deploykit.SaveVmDeployState/RemoveVmDeployEntry directly with its own
// lock + marshal + reader).
