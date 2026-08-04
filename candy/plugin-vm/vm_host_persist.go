package vm

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// vm_host_persist.go — the PLUGIN-SIDE deploy-ledger persist path for command:vm, relocated from
// the deleted host_build_config_resolve.go hostBuildConfigPersist host-builder (#55 coneC-dsh β2
// config-PERSIST shed). The "config-persist" HostBuild seam is GONE; the plugin calls
// deploykit.SaveVmDeployState / RemoveVmDeployEntry directly, supplying its OWN two primitives
// the deleted host-builder injected (saveBundleConfigNodeForm + the nil reader):
//   - vmLoadBundleConfig: the cycle-free loader-backed reader (loaderkit.LoadHostBundleConfigViaExecutor,
//     placement-invariant — the SAME reader candy/plugin-bundle's writes use, R3).
//   - vmMarshalNode: the node-form marshal, resugaring each plan step via the loader-threaded
//     Primaries (the "loader-threaded" HostBuild seam — the SAME D-fact the deleted host
//     saveBundleConfigNodeForm fed deploykit.MarshalBundleNode via loaderThreaded().Primaries).
//
// The THIRD primitive the host-builder used to inject — the process-shared deploy-config flock —
// is no longer injected at all: both writers run inside deploykit.MutateBundleConfig, THE one
// locked read-modify-write cycle every overlay writer shares, so this file's own private lock copy
// is deleted (R3 — three identical copies were what let the pod config-setup path ship with none).
//
// The VM-SPECIFIC decision logic (ephemeral-state preserve merge, auto-vs-operator delete, stale
// dotted-twin prune) stays in deploykit.SaveVmDeployState/RemoveVmDeployEntry — unchanged.

// vmLoadBundleConfig is the plugin's loader-backed reader for the per-host deploy overlay — the
// cycle-free loaderkit.LoadHostBundleConfigViaExecutor read (placement-invariant, works identically
// compiled-in or out-of-process). The SAME reader candy/plugin-bundle's writes use (#55 coneC Unit
// C2 — the reader-callback precedent). Returns (nil, nil) on an absent/empty overlay, matching
// deploykit.LoadBundleConfig's own contract.
func vmLoadBundleConfig() (*deploykit.BundleConfig, error) {
	return loaderkit.LoadHostBundleConfigViaExecutor(cmdCtx, cmdExec)
}

// vmFetchLoaderPrimaries returns the loader-threaded Primaries DATA snapshot (plugin-verb WORD →
// scalar-sugar primary field — the SAME registry-derived map candy/plugin-bundle's
// fetchLoaderPrimaries reads, and the host's deleted saveBundleConfigNodeForm fed
// deploykit.MarshalBundleNode via loaderThreaded().Primaries). The node-form deploy-state WRITE
// reads it here so command:vm resugars each plan step PLUGIN-SIDE. A HostBuild failure degrades to
// an empty map (a plan with no plugin-verb sugar marshals identically).
func vmFetchLoaderPrimaries() map[string]string {
	if cmdExec == nil {
		return nil
	}
	out, err := cmdExec.HostBuild(cmdCtx, "loader-threaded", nil)
	if err != nil {
		return nil
	}
	var t spec.Threaded
	if err := json.Unmarshal(out, &t); err != nil {
		return nil
	}
	return t.Primaries
}

// vmMarshalNode builds the per-entry node-form marshal callback deploykit.SaveBundleConfig takes.
// It resugars each plan step via the loader-threaded Primaries snapshot (vmFetchLoaderPrimaries) —
// the SAME marshal the deleted host saveBundleConfigNodeForm performed (deploykit.MarshalBundleNode,
// primaries threaded as DATA).
func vmMarshalNode() func(string, *deploykit.BundleNode) (*yaml.Node, error) {
	primaries := vmFetchLoaderPrimaries()
	return func(_ string, node *deploykit.BundleNode) (*yaml.Node, error) {
		return deploykit.MarshalBundleNode(node, primaries)
	}
}

// vmSaveDeployConfig persists dc PLUGIN-SIDE via deploykit.SaveBundleConfig — the save callback
// deploykit.SaveVmDeployState / RemoveVmDeployEntry take, supplying vmMarshalNode + the
// loader-backed reader (the fail-safe re-read SaveBundleConfig performs).
func vmSaveDeployConfig(dc *deploykit.BundleConfig) error {
	return deploykit.SaveBundleConfig(dc, vmMarshalNode(), vmLoadBundleConfig)
}

// hostConfigPersist saves (or, with remove, deletes) an entity's deploy-ledger entry PLUGIN-SIDE
// under the deploy-config lock. key is the full deploy key ("vm:<name>"). This replaces the deleted
// HostBuild("config-persist") host-builder round-trip — the plugin now drives
// deploykit.SaveVmDeployState/RemoveVmDeployEntry directly with its own save + reader (the lock is
// deploykit.MutateBundleConfig's, shared with every other overlay writer).
func hostConfigPersist(key, entity string, st *spec.VmDeployState, remove bool) error {
	if cmdExec == nil {
		return fmt.Errorf("config-persist: no host reverse channel (command not compiled-in?)")
	}
	if key == "" {
		return fmt.Errorf("config-persist: empty deploy key")
	}
	if remove {
		return deploykit.RemoveVmDeployEntry(key, vmSaveDeployConfig, vmLoadBundleConfig)
	}
	return deploykit.SaveVmDeployState(key, entity, st, vmSaveDeployConfig, vmLoadBundleConfig)
}
