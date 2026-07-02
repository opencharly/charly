// Command plugin-feature is the OUT-OF-TREE charly COMMAND-class plugin serving the
// externalized `charly feature …` CLI — the plan-shaped-description inspection surface
// (list / pending / validate) ported OUT of charly's core (the deleted
// charly/description_cmd.go's FeatureCmd + charly/plugin_command_feature.go), so the
// operator-facing `charly feature` command no longer compiles into the core binary.
//
// The THIRD WELDED-command externalization in the core-externalization program (after
// candy/plugin-tmux and candy/plugin-preempt). `charly feature` was WELDED to the DEEPEST
// core — its handlers call the unified loader (LoadConfig / ScanCandy), iterate the Step
// plan model (StepKind / Kind / IsAgent / KeywordText), and call validatePlanSteps, which
// is SHARED with `charly box validate` (validate.go), so the loader + plan model +
// validatePlanSteps MUST STAY CORE (R3). The SOLUTION (the preempt/tmux precedent — a plugin
// shells back through SANCTIONED charly CLI verbs, never ad-hoc anything, R4): this plugin
// re-expresses each `charly feature` leaf as a shell-back through three NEW HIDDEN core
// commands that do the loading + inspection in-core — `charly __feature-list`,
// `charly __feature-pending`, and `charly __feature-validate`. Those hidden verbs are the SAME
// `charly __cli-model` / `charly __plugin-providers` / `charly __preempt-status`
// internal-command pattern; the loader + plan model + validatePlanSteps stay core, invoked
// ONLY there. (The Feature RUN verbs are NOT part of this move — `charly box feature run`
// (image.go) and `charly check feature run` (check_cmd.go) stay children of box/check.)
// No core symbol crosses the process boundary; no ad-hoc podman/virsh.
//
// CLI dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly feature <args…>`, charly RESOLVES this plugin's binary (host-built from source, or
// baked into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the
// pass-through tokens after the `feature` word, in CLI mode (the go-plugin handshake cookie is
// stripped, so sdk.Main runs cliMain instead of serving gRPC) with CHARLY_BIN stamped to
// charly's own executable — so every shell-back re-enters the SAME charly binary that
// dispatched the plugin. The plugin owns real terminal stdio, so the inspection output reaches
// the operator's terminal natively.
//
// A command is NOT a gRPC-registry capability (charly fork/execs the binary; it never
// connects over gRPC for a command), so this plugin advertises NO Describe capability — its
// serve half (sdk.Serve, never reached for a command-only plugin) exists only to satisfy the
// dual-mode sdk.Main signature. The candy's plugin.providers declaration still lists
// command:feature (that drives the CLI-grammar prescan + the baked `.providers` manifest).
package main

import (
	"embed"

	"github.com/opencharly/charly/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// main is dual-mode (sdk.Main). For a command-only plugin, charly only ever fork/execs the
// binary (CLI mode) — it never launches it in go-plugin serve mode — so cliMain is the live
// path; the serve half is inert (no gRPC capability).
func main() { sdk.Main(&provider{}, &meta{}, cliMain) }
