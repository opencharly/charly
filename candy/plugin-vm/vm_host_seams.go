package vm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// vm_host_seams.go — the command:vm plugin's bridge to the host. The VM CLI handlers moved out of
// charly core (P10); the config loader + runtime-settings store + deploy ledger + egress subsystem
// are core Mechanisms a plugin cannot import (separate module), so the handlers reach them over the
// in-proc reverse channel: config → HostBuild("config-resolve"), ledger writes → HostBuild(
// "config-persist"), egress → InvokeProvider(verb:egress). command:vm is COMPILED-IN and dispatches
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
// JSON envelopes (VmJSON/ResourcesJSON); hostConfigResolve unmarshals them back into their typed
// values here so the moved handlers reference reply.VM / reply.Resources exactly as before. The other
// fields — including VmState (*VmDeployState) and ClaimantNode (*Deploy), whose types ARE CUE-referenced
// and cross the wire directly — pass through unchanged.
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
// resolveVmBackend/lookupVMClaimant + #Vm defaults + the persisted VmState) — the READ seam. It decodes
// the opaque VmJSON/ResourcesJSON envelopes into the typed *VmSpec / resource map for the caller.
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
	cfg := resolvedConfig{
		Backend:      wire.Backend,
		Claimant:     wire.Claimant,
		ClaimantNode: wire.ClaimantNode,
		VmBackend:    wire.VmBackend,
		BuildEngine:  wire.BuildEngine,
		RunEngine:    wire.RunEngine,
		VmState:      wire.VmState,
		VmEntities:   wire.VmEntities,
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

// hostConfigPersist saves (or, with remove, deletes) an entity's deploy-ledger entry host-side under
// the core deploy-config lock — the WRITE seam. key is the full deploy key ("vm:<name>").
func hostConfigPersist(key, entity string, st *spec.VmDeployState, remove bool) error {
	if cmdExec == nil {
		return fmt.Errorf("config-persist: no host reverse channel (command not compiled-in?)")
	}
	reqJSON, err := json.Marshal(spec.ConfigPersistRequest{Key: key, Entity: entity, VmState: st, Remove: remove})
	if err != nil {
		return err
	}
	if _, err := cmdExec.HostBuild(cmdCtx, "config-persist", reqJSON); err != nil {
		return err
	}
	return nil
}
