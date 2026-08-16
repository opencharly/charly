package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

// zzCmdSeamProbeCmd is a fake subcommand used only to exercise the command seam.
type zzCmdSeamProbeCmd struct{}

func (zzCmdSeamProbeCmd) Run() error { return nil }

// zzCmdSeamProv is a fake COMMAND-class provider contributing zzCmdSeamProbeCmd.
type zzCmdSeamProv struct{ builtinCommandBase }

func (zzCmdSeamProv) Reserved() string { return "zz-cmd-seam-probe" }
func (zzCmdSeamProv) KongCommand() any {
	return &struct {
		ZzCmdSeamProbe zzCmdSeamProbeCmd `cmd:"" help:"command-seam test probe"`
	}{}
}

// TestCommandSeam_PluginCommandInjected proves the 6th (COMMAND-class) provider seam:
// a registered CommandProvider's subcommand is collected (collectCommandPlugins) and
// embedded into the REAL charly CLI grammar via the kong.Plugins embed, so
// `charly zz-cmd-seam-probe` parses and selects that command — exactly how a
// non-machinery command reaches the CLI once migrated into a provider (Phase 1-4).
// The test FAILS if the seam does not wire the provider's command into the root.
func TestCommandSeam_PluginCommandInjected(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	RegisterBuiltinProvider(zzCmdSeamProv{})

	var cli CLI
	cli.Plugins = collectCommandPlugins()
	parser, err := kong.New(&cli, kong.Name("charly"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatalf("kong.New with the command-plugin seam failed: %v", err)
	}
	ctx, err := parser.Parse([]string{"zz-cmd-seam-probe"})
	if err != nil {
		t.Fatalf("plugin command not injected into the CLI grammar: %v", err)
	}
	if got := ctx.Command(); got != "zz-cmd-seam-probe" {
		t.Fatalf("expected the plugin command selected, got %q", got)
	}
}

// TestCommandCompileIn_ExampleCommandInProc proves F8's command compile-in bridge: the
// candy/plugin-example-command command candy, listed in compiled_plugins, registers IN-PROC as a
// ClassCommand inprocProvider (NOT a *grpcProvider, NOT a static builtin CommandProvider), so
// dispatchCommand routes `charly examplecommand` to it via Invoke(ops.OpRun) — the in-proc placement
// of a command candy, the LAST of the six classes to gain compiled-in placement. (End-to-end CLI
// dispatch is exercised by the live `charly examplecommand` proof + the check-pod bed.)
func TestCommandCompileIn_ExampleCommandInProc(t *testing.T) {
	prov, ok := providerRegistry.resolve(ClassCommand, "examplecommand")
	if !ok {
		t.Fatal("compiled-in command candy plugin-example-command did not register command:examplecommand (pluginsgen/compiled_plugins)")
	}
	if _, isGrpc := prov.(*grpcProvider); isGrpc {
		t.Fatal("examplecommand registered as a *grpcProvider — expected an in-proc inprocProvider (compiled-in placement)")
	}
	if _, isInproc := prov.(*inprocProvider); !isInproc {
		t.Fatalf("examplecommand provider is %T, want *inprocProvider (compiled-in command, dispatched in-proc)", prov)
	}
	if _, isCmdProv := prov.(CommandProvider); isCmdProv {
		t.Fatal("examplecommand should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(ops.OpRun))")
	}
}

// TestCommandCompileIn_AliasInProc proves the P14 alias extraction: `charly alias …`, formerly a
// dedicated builtin CommandProvider, is now the compiled-in command candy candy/plugin-alias —
// registered IN-PROC as a ClassCommand inprocProvider (NOT a *grpcProvider, NOT a static builtin
// CommandProvider), so dispatchCommand routes `charly alias` to it via Invoke(ops.OpRun) and its
// `add`/`install` handlers reach the host over the HostBuild("cli") reverse channel. (End-to-end
// CLI dispatch is exercised by the live check-commands-local bed + the plugin's Go tests.)
func TestCommandCompileIn_AliasInProc(t *testing.T) {
	prov, ok := providerRegistry.resolve(ClassCommand, "alias")
	if !ok {
		t.Fatal("compiled-in command candy plugin-alias did not register command:alias (pluginsgen/compiled_plugins)")
	}
	if _, isGrpc := prov.(*grpcProvider); isGrpc {
		t.Fatal("alias registered as a *grpcProvider — expected an in-proc inprocProvider (compiled-in placement)")
	}
	if _, isInproc := prov.(*inprocProvider); !isInproc {
		t.Fatalf("alias provider is %T, want *inprocProvider (compiled-in command, dispatched in-proc)", prov)
	}
	if _, isCmdProv := prov.(CommandProvider); isCmdProv {
		t.Fatal("alias should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(ops.OpRun))")
	}
}

// TestCommandCompileIn_StatusInProc proves the P14a chunk 2b status extraction: `charly status
// …`, formerly a dedicated builtin CommandProvider (the plugin_command_status.go registration,
// deleted), is now the compiled-in command candy candy/plugin-status — registered IN-PROC as a
// ClassCommand inprocProvider (NOT a *grpcProvider, NOT a static builtin CommandProvider), so
// dispatchCommand routes `charly status` to it via Invoke(ops.OpRun) and its render/overlay logic
// reaches the compiled-in verb:status-fanout by InvokeProvider'ing it directly over the reverse channel.
// (End-to-end CLI dispatch is exercised by the live R10 bed + the candy's own
// overlay_golden_test.go byte-parity golden.)
func TestCommandCompileIn_StatusInProc(t *testing.T) {
	prov, ok := providerRegistry.resolve(ClassCommand, "status")
	if !ok {
		t.Fatal("compiled-in command candy plugin-status did not register command:status (pluginsgen/compiled_plugins)")
	}
	if _, isGrpc := prov.(*grpcProvider); isGrpc {
		t.Fatal("status registered as a *grpcProvider — expected an in-proc inprocProvider (compiled-in placement)")
	}
	if _, isInproc := prov.(*inprocProvider); !isInproc {
		t.Fatalf("status provider is %T, want *inprocProvider (compiled-in command, dispatched in-proc)", prov)
	}
	if _, isCmdProv := prov.(CommandProvider); isCmdProv {
		t.Fatal("status should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(ops.OpRun))")
	}
}

// TestCommandCompileIn_SshInProc proves the #118 loader+check-tail ssh extraction: `charly ssh
// tunnel spice/vnc`, formerly a dedicated builtin CommandProvider (the plugin_command_ssh.go
// registration, deleted), is now the compiled-in command candy candy/plugin-ssh — registered
// IN-PROC as a ClassCommand inprocProvider (NOT a *grpcProvider, NOT a static builtin
// CommandProvider), so dispatchCommand routes `charly ssh` to it via Invoke(ops.OpRun) and its tunnel
// handler reaches verb:libvirt by InvokeProvider'ing it directly over the reverse channel. (The
// live SSH-forwarded SPICE/VNC tunnel needs a remote-libvirt VM + interactive SIGINT, exercised
// manually / by the vm roster.)
func TestCommandCompileIn_SshInProc(t *testing.T) {
	prov, ok := providerRegistry.resolve(ClassCommand, "ssh")
	if !ok {
		t.Fatal("compiled-in command candy plugin-ssh did not register command:ssh (pluginsgen/compiled_plugins)")
	}
	if _, isGrpc := prov.(*grpcProvider); isGrpc {
		t.Fatal("ssh registered as a *grpcProvider — expected an in-proc inprocProvider (compiled-in placement)")
	}
	if _, isInproc := prov.(*inprocProvider); !isInproc {
		t.Fatalf("ssh provider is %T, want *inprocProvider (compiled-in command, dispatched in-proc)", prov)
	}
	if _, isCmdProv := prov.(CommandProvider); isCmdProv {
		t.Fatal("ssh should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(ops.OpRun))")
	}
}

// TestCommandCompileIn_CmdInProc proves the #118 loader+check-tail cmd extraction: `charly cmd`,
// formerly a dedicated builtin CommandProvider (the plugin_command_cmd.go registration, deleted), is
// now the compiled-in command candy candy/plugin-cmd — registered IN-PROC as a ClassCommand
// inprocProvider (NOT a *grpcProvider, NOT a static builtin CommandProvider), so dispatchCommand
// routes `charly cmd` to it via Invoke(ops.OpRun). The plugin owns the CLI grammar + the completion
// notification and drives the deploy-lifecycle-coupled interactive exec via the hidden `charly __cmd`
// core reentry over HostBuild("cli"); the __cmd handler (cmd.go) stays core as deploy-lifecycle
// RESIDUE. `cmd` was the LAST static-builtin deploy-lifecycle command — start/stop/restart/logs/
// remove/shell/service/volume/cp/config/update already moved to candy/plugin-pod and reap-orphans to
// candy/plugin-substrate — so the former TestCommandProviders_DeployLifecycleCommands is retired.
func TestCommandCompileIn_CmdInProc(t *testing.T) {
	prov, ok := providerRegistry.resolve(ClassCommand, "cmd")
	if !ok {
		t.Fatal("compiled-in command candy plugin-cmd did not register command:cmd (pluginsgen/compiled_plugins)")
	}
	if _, isGrpc := prov.(*grpcProvider); isGrpc {
		t.Fatal("cmd registered as a *grpcProvider — expected an in-proc inprocProvider (compiled-in placement)")
	}
	if _, isInproc := prov.(*inprocProvider); !isInproc {
		t.Fatalf("cmd provider is %T, want *inprocProvider (compiled-in command, dispatched in-proc)", prov)
	}
	if _, isCmdProv := prov.(CommandProvider); isCmdProv {
		t.Fatal("cmd should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(ops.OpRun))")
	}
}

// TestCommandCompileIn_PodInProc proves the DEPLOY wave's CLI-struct port: `charly start`/
// `stop`/`restart`/`logs`/`remove`/`shell`/`service`/`volume`/`cp`/`config`/`update`, formerly
// dedicated builtin CommandProviders (plugin_command_start.go / plugin_command_stop.go /
// plugin_command_restart.go / plugin_command_logs.go / plugin_command_remove.go /
// plugin_command_shell.go / plugin_command_service.go / plugin_command_volume.go /
// plugin_command_cp.go / plugin_command_config.go / plugin_command_update.go, deleted), are
// now the compiled-in command candy candy/plugin-pod — registered IN-PROC as ClassCommand
// inprocProviders (NOT *grpcProvider, NOT a static builtin CommandProvider), so
// dispatchCommand routes each to it via Invoke(ops.OpRun): restart/volume/cp call sdk/kit +
// sdk/deploykit directly (no host seam — pure logic, no core-only type or registry need).
// Cutover B unit 2 (pod-lifecycle-CLI-dispatch): start/stop/logs/shell/update/service/remove now
// ALL own their orchestration in-plugin, reaching the host only for the irreducible
// ResolveTarget + live-executor dispatch (start/stop/logs/shell/update/service) or the two
// genuinely host-coupled remove axes (credential env, deploy-entry cleanup) + the
// CHARLY_PREEMPT_LEASE-gated arbiter release (remove) — via the CONSOLIDATED
// host_build_pod_lifecycle_dispatch.go (replacing the former per-verb podStartCmd/podStopCmd/
// podLogsCmd/podShellCmd/podUpdateCmd/podServiceCmd/podRemoveCmd core reconstructions for all
// seven verbs) — over the ONE op-discriminated HostBuild("pod-lifecycle") kind (#55 W3 A10b
// unified the former eight dedicated per-verb kinds — HostBuild("pod-start")/("pod-stop")/
// ("pod-logs")/("pod-remove")/("pod-shell")/("pod-service")/("pod-update")/("pod-cmd") — into
// this one, discriminated by req.Op).
// Setup/Remove dispatch deploy:pod's sdk.OpConfigSetup/OpConfigRemove (the P13-KERNEL
// direction-flip) peer-to-peer via InvokeProvider (the "pod-config-setup"/"pod-config-remove"
// host-build seams are DELETED, K-wave 2 cone R3 — the SAME pattern candy/plugin-fleet/
// from_box_pod.go proves for the source-less from-box path). command:config's other four leaves —
// Status/Mount/Unmount/Passwd — no longer route through core at all (wave γ): candy/plugin-pod's
// enc_cmd.go dispatches verb:enc/verb:credential DIRECTLY via InvokeProvider, the same
// ALREADY-LIVE pattern candy/plugin-deploy-pod/lifecycle.go proves for the start/stop path.
// (End-to-end CLI dispatch is exercised live — see the DEPLOY wave report — and by the R10 bed roster.)
func TestCommandCompileIn_PodInProc(t *testing.T) {
	for _, word := range []string{"start", "stop", "restart", "logs", "remove", "shell", "service", "volume", "cp", "config", "update"} {
		prov, ok := providerRegistry.resolve(ClassCommand, word)
		if !ok {
			t.Fatalf("compiled-in command candy plugin-pod did not register command:%s (pluginsgen/compiled_plugins)", word)
		}
		if _, isGrpc := prov.(*grpcProvider); isGrpc {
			t.Fatalf("%s registered as a *grpcProvider — expected an in-proc inprocProvider (compiled-in placement)", word)
		}
		if _, isInproc := prov.(*inprocProvider); !isInproc {
			t.Fatalf("%s provider is %T, want *inprocProvider (compiled-in command, dispatched in-proc)", word, prov)
		}
		if _, isCmdProv := prov.(CommandProvider); isCmdProv {
			t.Fatalf("%s should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(ops.OpRun))", word)
		}
	}
}

// TestCommandCompileIn_ReapOrphansInProc proves the K5 status-subsystem relocation:
// `charly reap-orphans`, formerly a dedicated builtin CommandProvider
// (plugin_command_reap_orphans.go, deleted), is now served by the compiled-in
// candy/plugin-substrate (command:reap-orphans, alongside its existing kind:pod/vm/kubernetes/
// local/android + OpStatusCollect capabilities) — registered IN-PROC as a ClassCommand
// inprocProvider (NOT a *grpcProvider, NOT a static builtin CommandProvider), so
// dispatchCommand routes `charly reap-orphans` to it via Invoke(ops.OpRun) and its liveness
// probes reach the verb:libvirt peer provider over InvokeProvider (F10) instead of the
// deleted core-private invokeVmPlugin. (End-to-end CLI dispatch is exercised by the live
// R10 bed.)
func TestCommandCompileIn_ReapOrphansInProc(t *testing.T) {
	prov, ok := providerRegistry.resolve(ClassCommand, "reap-orphans")
	if !ok {
		t.Fatal("compiled-in command candy plugin-substrate did not register command:reap-orphans (pluginsgen/compiled_plugins)")
	}
	if _, isGrpc := prov.(*grpcProvider); isGrpc {
		t.Fatal("reap-orphans registered as a *grpcProvider — expected an in-proc inprocProvider (compiled-in placement)")
	}
	if _, isInproc := prov.(*inprocProvider); !isInproc {
		t.Fatalf("reap-orphans provider is %T, want *inprocProvider (compiled-in command, dispatched in-proc)", prov)
	}
	if _, isCmdProv := prov.(CommandProvider); isCmdProv {
		t.Fatal("reap-orphans should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(ops.OpRun))")
	}
}

// TestCommandProviders_ExtractedReachMCP proves the builtin-only CLI model (buildCLIModel's
// modelCLI, fed by collectCommandPlugins() exactly as the real CLI is) EXCLUDES every command that
// became a dynamic command candy with NO declared subcommand catalog — a compiled-in or external
// command with no catalog is an opaque pass-through Args holder (collectExternalCommandPlugins),
// never a builtin CommandProvider, so its subcommand leaves are NOT reflected into the
// out-of-process MCP bridge's (candy/plugin-mcp) tool surface. `ssh` is now the compiled-in command
// candy candy/plugin-ssh (command:ssh) that DECLARES a subcommand catalog (like `check` below), so
// it stays reflected via declaringCommandHolders; its in-proc registration is covered by
// TestCommandCompileIn_SshInProc. `mcp` itself is correctly absent (the MCP server does not expose "start an MCP server" as one of
// its own tools). `check` is the ONE exception (F-CLI-NEST, asserted separately below): it DOES
// declare a subcommand catalog, so buildCLIModel folds its holder in via declaringCommandHolders
// and its children — check.box included — ARE reflected, restoring the MCP tool discoverability
// this test used to assert was permanently lost.
func TestCommandProviders_ExtractedReachMCP(t *testing.T) {
	paths := cliModelLeafPaths(t)
	if paths["alias.list"] {
		t.Error("alias.list unexpectedly present in the builtin CLI model — `alias` is now a compiled-in command (candy/plugin-alias, command:alias), a dynamic holder not a builtin CommandProvider")
	}
	if paths["mcp.serve"] {
		t.Error("mcp.serve unexpectedly present in the builtin CLI model — `mcp` is now an external command (candy/plugin-mcp), not a builtin CommandProvider")
	}
	if paths["secrets.list"] {
		t.Error("secrets.list unexpectedly present in the builtin CLI model — `secrets` is now an external command (candy/plugin-secrets), not a builtin CommandProvider")
	}
	if !paths["tmux.list"] || !paths["tmux.run"] {
		t.Error("typed-provider tmux compatibility command model is missing from MCP reflection")
	}
	if paths["preempt.status"] {
		t.Error("preempt.status unexpectedly present in the builtin CLI model — `preempt` is now an external command (candy/plugin-preempt, the second welded-command externalization), not a builtin CommandProvider")
	}
	if paths["feature.list"] {
		t.Error("feature.list unexpectedly present in the builtin CLI model — `feature` is now an external command (candy/plugin-feature, the third welded-command externalization), not a builtin CommandProvider")
	}
	// clean + settings + candy are now COMPILED-IN and OWN their commands (candy/plugin-clean reaches
	// the shared core engine over the "retention" HostBuild seam; candy/plugin-settings owns its config
	// subsystem directly, the "settings" seam retired; candy owns its yaml.Node logic itself, sharing
	// only the generic kit.SetByDotPath / kit.MappingChild — no hidden core command for any). All are
	// absent from this builtin-only model — a compiled-in command
	// is a DYNAMIC holder (collectExternalCommandPlugins), never a builtin CommandProvider.
	// NOTE: `version` is DELIBERATELY NOT here — it was excluded from C15 (the distro repos'
	// packaging workflows stamp the package version via `bin/charly version`), so it stays a CORE
	// command and IS present in the builtin model (asserted by TestCLIModel_CoversCommands).
	if paths["clean"] {
		t.Error("clean unexpectedly present in the builtin CLI model — `clean` is now a compiled-in command (candy/plugin-clean, command:clean), a dynamic holder not a builtin CommandProvider")
	}
	if paths["settings.list"] {
		t.Error("settings.list unexpectedly present in the builtin CLI model — `settings` is now a compiled-in command (candy/plugin-settings, command:settings), a dynamic holder not a builtin CommandProvider")
	}
	if paths["candy.set"] {
		t.Error("candy.set unexpectedly present in the builtin CLI model — `candy` is now a compiled-in command (candy/plugin-candy, command:candy), a dynamic holder not a builtin CommandProvider")
	}
	// status (P14a chunk 2b) is likewise now COMPILED-IN and OWNS its command
	// (candy/plugin-status reaches the compiled-in verb:status-fanout by InvokeProvider'ing it
	// directly) — absent from this builtin-only model, a flat leaf
	// command with no subcommands, so its CLI-model path is bare "status" (mirrors "clean").
	if paths["status"] {
		t.Error("status unexpectedly present in the builtin CLI model — `status` is now a compiled-in command (candy/plugin-status, command:status), a dynamic holder not a builtin CommandProvider")
	}
	// reap-orphans (K5) is likewise now COMPILED-IN and OWNS its command (candy/plugin-substrate,
	// alongside its existing substrate-liveness collectors) — absent from this builtin-only model,
	// a flat leaf command with no subcommands, so its CLI-model path is bare "reap-orphans".
	if paths["reap-orphans"] {
		t.Error("reap-orphans unexpectedly present in the builtin CLI model — `reap-orphans` is now a compiled-in command (candy/plugin-substrate, command:reap-orphans), a dynamic holder not a builtin CommandProvider")
	}
	// check DOES declare a subcommand catalog (F-CLI-NEST), unlike every command above — its
	// children are DELIBERATELY reflected (both for `--help` fidelity and for MCP tool
	// generation, the two things this whole fix restores); see TestBuildCLIModel_CheckAndBoxList
	// for the positive assertion. Asserting its ABSENCE here would re-encode the very regression
	// this cutover fixes.
	if !paths["check.box"] || !paths["check.live"] {
		t.Error("check.box / check.live expected present in the builtin CLI model — `check` declares a subcommand catalog (F-CLI-NEST) and its children ARE reflected")
	}
}
