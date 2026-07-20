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
// dispatchCommand routes `charly examplecommand` to it via Invoke(OpRun) — the in-proc placement
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
		t.Fatal("examplecommand should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(OpRun))")
	}
}

// TestCommandCompileIn_AliasInProc proves the P14 alias extraction: `charly alias …`, formerly a
// dedicated builtin CommandProvider, is now the compiled-in command candy candy/plugin-alias —
// registered IN-PROC as a ClassCommand inprocProvider (NOT a *grpcProvider, NOT a static builtin
// CommandProvider), so dispatchCommand routes `charly alias` to it via Invoke(OpRun) and its
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
		t.Fatal("alias should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(OpRun))")
	}
}

// TestCommandCompileIn_StatusInProc proves the P14a chunk 2b status extraction: `charly status
// …`, formerly a dedicated builtin CommandProvider (the plugin_command_status.go registration,
// deleted), is now the compiled-in command candy candy/plugin-status — registered IN-PROC as a
// ClassCommand inprocProvider (NOT a *grpcProvider, NOT a static builtin CommandProvider), so
// dispatchCommand routes `charly status` to it via Invoke(OpRun) and its render/overlay logic
// reaches the host collection engine over the HostBuild("status-substrate") reverse channel.
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
		t.Fatal("status should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(OpRun))")
	}
}

// TestCommandProviders_ExtractedLeafCommands proves every leaf-domain command extracted
// into a dedicated COMMAND-class provider (ssh — the builtin leaf-domain
// batch) is (1) registered in providerRegistry as a CommandProvider with the matching
// Reserved() word, and (2) collected by collectCommandPlugins() and injected into the REAL
// charly CLI grammar via kong.Plugins, so its subcommand path parses and selects exactly as
// before the extraction. The test FAILS if any dedicated registration regresses or the
// command seam stops wiring one of them into the root.
func TestCommandProviders_ExtractedLeafCommands(t *testing.T) {
	assertCommandProviderInjected(t, []commandProviderCase{
		{"ssh", []string{"ssh", "tunnel", "spice", "myvm"}, "ssh tunnel spice <vm>"},
		// `mcp`, `secrets`, `udev`, `preempt`, `feature`, and `alias` are intentionally
		// absent: `charly mcp serve` (C1), `charly secrets …` (C2), `charly udev …`,
		// `charly preempt …` (the second welded-command externalization),
		// `charly feature …` (the third), and `charly alias …` (P14, candy/plugin-alias — COMPILED-IN)
		// are now dynamic command candies served by their own plugin (candy/plugin-mcp /
		// candy/plugin-secrets / candy/plugin-udev / candy/plugin-preempt /
		// candy/plugin-feature / candy/plugin-alias), NOT builtin CommandProviders. alias's
		// compiled-in in-proc registration is asserted by TestCommandCompileIn_AliasInProc.
	})
}

// TestCommandProviders_DeployLifecycleCommands proves every deploy-lifecycle + remaining
// leaf command extracted into a dedicated COMMAND-class provider (the deploy-lifecycle
// batch: start/stop/restart/update/remove/logs/shell/cmd/cp/volume/service/config) is
// (1) registered in providerRegistry as a CommandProvider with the
// matching Reserved() word, and (2) collected by collectCommandPlugins() and injected into
// the REAL charly CLI grammar via kong.Plugins, so its subcommand path parses and selects
// exactly as before the extraction (the Run handler — which calls the unchanged core
// deploy machinery — is preserved verbatim). The test FAILS if any dedicated
// registration regresses or the command seam stops wiring one of them into the root.
// (`bundle` is no longer here — `charly bundle …` is now a dynamic command served by
// candy/plugin-bundle (compiled-in), dispatched in-proc via Invoke(OpRun) rather than
// through a builtin CommandProvider, exactly like vm/feature. `status` is no longer here
// either — P14a chunk 2b externalized it to the compiled-in candy/plugin-status
// (command:status), the SAME dynamic in-proc bridge alias/settings/clean/candy use; its
// compiled-in registration is asserted by TestCommandCompileIn_StatusInProc. `reap-orphans`
// is no longer here either — K5 relocated it to the compiled-in candy/plugin-substrate
// (command:reap-orphans, alongside its existing substrate-liveness collectors), the SAME
// dynamic in-proc bridge; its compiled-in registration is asserted by
// TestCommandCompileIn_ReapOrphansInProc.)
func TestCommandProviders_DeployLifecycleCommands(t *testing.T) {
	assertCommandProviderInjected(t, []commandProviderCase{
		{"start", []string{"start", "mybox"}, "start <box>"},
		{"stop", []string{"stop", "mybox"}, "stop <box>"},
		{"restart", []string{"restart", "mybox"}, "restart <box>"},
		{"update", []string{"update", "mybox"}, "update <box>"},
		{"remove", []string{"remove", "mybox"}, "remove <box>"},
		{"logs", []string{"logs", "mybox"}, "logs <box>"},
		{"shell", []string{"shell", "mybox"}, "shell <box>"},
		{"cmd", []string{"cmd", "mybox", "echo hi"}, "cmd <box> <command>"},
		{"cp", []string{"cp", "mybox", ":/a", "/b"}, "cp <box> <src> <dst>"},
		{"volume", []string{"volume", "list", "mybox"}, "volume list <box>"},
		{"service", []string{"service", "status", "mybox"}, "service status <box>"},
		{"config", []string{"config", "status", "mybox"}, "config status <box>"},
	})
}

// TestCommandCompileIn_ReapOrphansInProc proves the K5 status-subsystem relocation:
// `charly reap-orphans`, formerly a dedicated builtin CommandProvider
// (plugin_command_reap_orphans.go, deleted), is now served by the compiled-in
// candy/plugin-substrate (command:reap-orphans, alongside its existing kind:pod/vm/k8s/
// local/android + OpStatusCollect capabilities) — registered IN-PROC as a ClassCommand
// inprocProvider (NOT a *grpcProvider, NOT a static builtin CommandProvider), so
// dispatchCommand routes `charly reap-orphans` to it via Invoke(OpRun) and its liveness
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
		t.Fatal("reap-orphans should NOT be a static CommandProvider — a compiled-in command candy uses the dynamic in-proc command bridge (dispatchCommand → Invoke(OpRun))")
	}
}

// commandProviderCase is one case for assertCommandProviderInjected: a Reserved() word, the
// argv that selects its (sub)command, and the expected ctx.Command() after parse.
type commandProviderCase struct {
	word     string   // Reserved() + top-level command name
	parse    []string // argv selecting the command (or a leaf subcommand)
	selected string   // expected ctx.Command() after parse
}

// assertCommandProviderInjected proves each case's command is (1) registered in
// providerRegistry as a CommandProvider with the matching Reserved() word, and (2) collected
// by collectCommandPlugins() and injected into the REAL charly CLI grammar via kong.Plugins,
// so its subcommand path parses and selects exactly as authored. Shared by the extracted-leaf
// / deploy-lifecycle command tests (R3).
func assertCommandProviderInjected(t *testing.T, cases []commandProviderCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.word, func(t *testing.T) {
			// 1. Registered as a COMMAND-class provider, resolvable through the registry.
			p, ok := providerRegistry.resolve(ClassCommand, tc.word)
			if !ok {
				t.Fatalf("command:%s not registered — dedicated self-registration regressed", tc.word)
			}
			cp, ok := p.(CommandProvider)
			if !ok {
				t.Fatalf("%s provider is not a CommandProvider (got %T)", tc.word, p)
			}
			if cp.Reserved() != tc.word {
				t.Fatalf("%s provider Reserved() = %q, want %q", tc.word, cp.Reserved(), tc.word)
			}

			// 2. Collected by the command seam and injected into the real CLI grammar.
			var cli CLI
			cli.Plugins = collectCommandPlugins()
			parser, err := kong.New(&cli, kong.Name("charly"), kong.Exit(func(int) {}))
			if err != nil {
				t.Fatalf("kong.New with the command-plugin seam failed: %v", err)
			}
			ctx, err := parser.Parse(tc.parse)
			if err != nil {
				t.Fatalf("%s command not injected into the CLI grammar: %v", tc.word, err)
			}
			if got := ctx.Command(); got != tc.selected {
				t.Fatalf("expected %q selected, got %q", tc.selected, got)
			}
		})
	}
}

// TestCommandProviders_ExtractedReachMCP proves the builtin-only CLI model (buildCLIModel's
// modelCLI, fed by collectCommandPlugins() exactly as the real CLI is) EXCLUDES every command that
// became a dynamic command candy — a compiled-in or external command is a pass-through Args holder
// (collectExternalCommandPlugins), never a builtin CommandProvider, so its subcommand leaves are
// NOT reflected into the out-of-process MCP bridge's (candy/plugin-mcp) tool surface. The
// still-builtin leaf-domain commands (ssh) that DO stay reflected are covered by
// TestCommandProviders_ExtractedLeafCommands; `mcp` itself is correctly absent (the MCP server does
// not expose "start an MCP server" as one of its own tools).
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
	// clean + settings + candy are now COMPILED-IN and OWN their commands (candy/plugin-clean +
	// candy/plugin-settings reach their shared core engine over a generic HostBuild seam; candy owns
	// its yaml.Node logic itself, sharing only the generic kit.SetByDotPath / kit.MappingChild — no
	// hidden core command for any). All are absent from this builtin-only model — a compiled-in command
	// is a DYNAMIC holder (collectExternalCommandPlugins), never a builtin CommandProvider.
	// NOTE: `version` is DELIBERATELY NOT here — it was excluded from C15 (pkg/arch's pkgver()
	// stamps the package version via `bin/charly version`), so it stays a CORE command and IS
	// present in the builtin model (asserted by TestCLIModel_CoversCommands).
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
	// (candy/plugin-status reaches the shared collection engine over the generic
	// HostBuild("status-substrate") seam) — absent from this builtin-only model, a flat leaf
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
	if paths["check.box"] {
		t.Error("check.box unexpectedly present in the builtin CLI model — `check` is now a compiled-in command (candy/plugin-check, command:check), a dynamic holder not a builtin CommandProvider")
	}
}
