// Package bundle is the charly plugin housing the `charly bundle …` deployment CLI. It is a
// dual-placement command plugin (F8) mirroring candy/plugin-vm: the SAME NewProvider()/NewMeta()/
// CliMain compile INTO charly in-process when listed in compiled_plugins (the canonical placement,
// P13), or cmd/serve serves them OUT-OF-PROCESS when they are not. It provides ONE capability —
//
//   - command:bundle — `charly bundle add / del / show / export / import / reset / path / status /
//     from-box`, the deployment CLI. COMPILED-IN, it dispatches IN-PROC via Invoke(OpRun)
//     (runBundleCommand → kong-parse the BundleCmd tree — command.go), so the handlers run in
//     charly's OWN process and inherit charly's real stdio/TTY natively. They compile InstallPlans
//     over sdk/deploykit (the P4 IR compiler) and reach the host-only Mechanisms — the config
//     loader + deploy ledger, and the deploy-dispatch kernel (ResolveTarget → externalDeployTarget
//     over the executor reverse channel) — over the generic fabric adapters: the config-resolve /
//     config-persist / deploy-apply / cli HostBuild seams and verb:egress / verb:arbiter.
//
// A standalone Go module (its own go.mod) importing ONLY the sdk module, compiled into charly for
// the canonical placement. The capability is advertised in Describe (NewMeta); command:bundle's
// grammar is prescanned into the CLI from plugin.providers.
package bundle

import (
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// calver is the plugin's advertised version; kept in lockstep with candy/plugin-bundle/charly.yml.
const calver = "2026.193.1200"

// NewProvider returns the bundle provider (command:bundle Invoke surface).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:bundle. The served schema carries no #*Input def — a command's args
// are pass-through CLI tokens, not a structured plugin_input — so the capability has no InputDef.
// command:bundle is COMPILED-IN and dispatched IN-PROC via Invoke(OpRun) (runBundleCommand,
// command.go); its grammar is prescanned into the CLI from plugin.providers.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "command", Word: "bundle"}},
		nil)
}

// provider is the out-of-process provider. Its Invoke dispatches command:bundle's OpRun (the
// `charly bundle …` CLI) in charly's own process when compiled-in.
type provider struct{ pb.UnimplementedProviderServer }
