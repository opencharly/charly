// Package fleet is the charly plugin housing the `charly fleet …` deployment CLI. It is a
// dual-placement command plugin (F8) mirroring candy/plugin-vm: the SAME NewProvider()/NewMeta()/
// CliMain compile INTO charly in-process when listed in compiled_plugins (the canonical placement,
// P13), or cmd/serve serves them OUT-OF-PROCESS when they are not. It provides ONE capability —
//
//   - command:fleet — `charly fleet add / del / show / export / import / reset / path / status /
//     from-box`, the deployment CLI. COMPILED-IN, it dispatches IN-PROC via Invoke(OpRun)
//     (runFleetCommand → kong-parse the FleetCmd tree — command.go), so the handlers run in
//     charly's OWN process and inherit charly's real stdio/TTY natively. `add`/`del` (walk.go, the
//     K4-C WALK PORT) drive the WHOLE deploy-tree walk plugin-side: the config loader
//     (the merged-tree read, LoadUnified-coupled) + the del RESOLUTION (del_resolve.go, K-wave 2
//     cone R2 bank C) run plugin-side; the registry-backed executor-chain
//     derivation (deriveChildExecutorForPath) stays host-side behind three narrow seams —
//     deploy-plugins-connect / resolve-target-add / deploy-node-del-dispatch —
//     while the tree traversal AND the per-node compile (compilePlansForRequest, IN-PROC after
//     K4-C shape-2 — no OpCompile round-trip) run plugin-side; ResolveTarget → the deploy target's
//     Add/Del is the host tail of the resolve-target-add / deploy-node-del-dispatch seams. Sibling
//     `peer:` member bring-up/tear-down calls sdk/deploykit.BringUpMembers/TearDownMembers
//     directly (#55 W3 A4 — the former deploy-members-up/-down seams are deleted). `from-box`
//     runs fully plugin-side since K-wave 2 cone R2 (runFromBoxPod reaches deploy:pod's
//     OpConfigSetup by direct InvokeProvider; the "deploy-from-box" seam is deleted); the
//     config-management leaves (show/export/import/reset/status) run plugin-side — reads via
//     loaderkit.LoadHostFleetConfigViaExecutor, writes via
//     deploykit.SaveFleetConfig directly (#55 K4 config-write seam-collapse). `path`
//     resolves plugin-side via kit.DefaultDeployConfigPath (no seam).
//
// A standalone Go module (its own go.mod) importing ONLY the sdk module, compiled into charly for
// the canonical placement. The capability is advertised in Describe (NewMeta); command:fleet's
// grammar is prescanned into the CLI from plugin.providers.
package fleet

import (
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// calver is the plugin's advertised version; kept in lockstep with candy/plugin-fleet/charly.yml.
const calver = "2026.193.1200"

// NewProvider returns the fleet provider (command:fleet Invoke surface).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:fleet. The served schema carries no #*Input def — a command's args
// are pass-through CLI tokens, not a structured plugin_input — so the capability has no InputDef.
// command:fleet is COMPILED-IN and dispatched IN-PROC via Invoke(OpRun) (runFleetCommand,
// command.go); its grammar is prescanned into the CLI from plugin.providers.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "command", Word: "fleet"}},
		nil)
}

// provider is the out-of-process provider. Its Invoke dispatches command:fleet's OpRun (the
// `charly fleet …` CLI) in charly's own process when compiled-in.
type provider struct{ pb.UnimplementedProviderServer }
