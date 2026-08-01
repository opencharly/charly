package vm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
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
	BundleNode          = spec.Deploy
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

// resolvedConfig is the plugin-facing decode of spec.ConfigResolveReply. The wire reply carries the
// two hand-written runtime types with no CUE def (*ResolvedVm, map[string]*ResolvedResource) as opaque
// JSON envelopes (VmJSON/ResourcesJSON) + the PROJECT deploy tree as BundleJSON; hostConfigResolve
// unmarshals them back into their typed values here so the moved handlers reference reply.VM /
// reply.Resources exactly as before. VmState (*VmDeployState) crosses the wire directly (the host
// reads it spec-only via LoadUnified of the per-host overlay). Claimant/ClaimantNode are computed
// PLUGIN-SIDE from BundleJSON (#55 coneC-dsh β2 config-RESOLVE: deploykit.MergedDeployTree +
// spec.FindVMClaimant over the plugin's loader-backed reader) — they no longer cross the wire.
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

// hostConfigResolve resolves the project config for an entity host-side (LoadUnified/ResolveRuntime/
// #Vm defaults + the persisted VmState) — the READ seam. It decodes the opaque
// VmJSON/ResourcesJSON/BundleJSON envelopes, computes the exclusive-resource Claimant PLUGIN-SIDE
// (#55 coneC-dsh β2 config-RESOLVE: the host no longer calls deploykit; it ships the PROJECT bundle
// as BundleJSON + the plugin merges the per-host overlay ITSELF via deploykit.MergedDeployTree +
// spec.FindVMClaimant, placement-invariant over loaderkit.LoadHostBundleConfigViaExecutor), and
// computes the effective VM backend ITSELF (resolveVmBackendPlugin/vmConfiguredBackendPlugin, F6
// vm-lifecycle move, vm_backend_resolve.go) — the "config-resolve" wire reply carries no Backend.
func hostConfigResolve(entity string) (resolvedConfig, error) {
	if cmdExec == nil {
		return resolvedConfig{}, fmt.Errorf("config-resolve: no host reverse channel (command not compiled-in?)")
	}
	reqJSON, err := json.Marshal(spec.ConfigResolveRequest{Entity: entity})
	if err != nil {
		return resolvedConfig{}, err
	}
	out, err := cmdExec.HostBuild(cmdCtx, "config-resolve", reqJSON)
	if err != nil {
		return resolvedConfig{}, err
	}
	var wire spec.ConfigResolveReply
	if err := json.Unmarshal(out, &wire); err != nil {
		return resolvedConfig{}, fmt.Errorf("config-resolve: decode reply: %w", err)
	}
	backend, err := resolveVmBackendPlugin(vmConfiguredBackendPlugin(cmdCtx, cmdExec, entity, wire.VmBackend))
	if err != nil {
		return resolvedConfig{}, err
	}
	cfg := resolvedConfig{
		Backend:     backend,
		VmBackend:   wire.VmBackend,
		BuildEngine: wire.BuildEngine,
		RunEngine:   wire.RunEngine,
		VmState:     wire.VmState,
		VmEntities:  wire.VmEntities,
	}
	// Claimant computation moved plugin-side (#55 coneC-dsh β2 config-RESOLVE): unmarshal the PROJECT
	// bundle the host ships as BundleJSON, merge the per-host overlay via deploykit.MergedDeployTree
	// (placement-invariant reader = loaderkit.LoadHostBundleConfigViaExecutor), + spec.FindVMClaimant.
	if len(wire.BundleJSON) > 0 {
		var project map[string]spec.BundleNode
		if err := json.Unmarshal(wire.BundleJSON, &project); err != nil {
			return resolvedConfig{}, fmt.Errorf("config-resolve: decode bundle: %w", err)
		}
		merged := deploykit.MergedDeployTree(project, "vm config-resolve", func() (*deploykit.BundleConfig, error) {
			return loaderkit.LoadHostBundleConfigViaExecutor(cmdCtx, cmdExec)
		})
		if claimant, claimantNode, hasClaimant := spec.FindVMClaimant(merged, entity); hasClaimant {
			cfg.Claimant = claimant
			cfg.ClaimantNode = &claimantNode
		}
	}
	if len(wire.VmJSON) > 0 {
		var vm VmSpec
		if err := json.Unmarshal(wire.VmJSON, &vm); err != nil {
			return resolvedConfig{}, fmt.Errorf("config-resolve: decode vm: %w", err)
		}
		cfg.VM = &vm
	}
	if len(wire.ResourcesJSON) > 0 {
		if err := json.Unmarshal(wire.ResourcesJSON, &cfg.Resources); err != nil {
			return resolvedConfig{}, fmt.Errorf("config-resolve: decode resources: %w", err)
		}
	}
	return cfg, nil
}

// hostConfigPersist now lives in vm_host_persist.go — the PLUGIN-SIDE deploy-ledger persist path
// (#55 coneC-dsh β2 config-PERSIST shed: the former HostBuild("config-persist") host-builder is
// deleted; the plugin calls deploykit.SaveVmDeployState/RemoveVmDeployEntry directly with its own
// lock + marshal + reader).
