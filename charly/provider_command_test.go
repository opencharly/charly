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

// TestCommandProviders_ExtractedLeafCommands proves every leaf-domain command extracted
// into a dedicated COMMAND-class provider (alias/ssh — the builtin leaf-domain
// batch) is (1) registered in providerRegistry as a CommandProvider with the matching
// Reserved() word, and (2) collected by collectCommandPlugins() and injected into the REAL
// charly CLI grammar via kong.Plugins, so its subcommand path parses and selects exactly as
// before the extraction. The test FAILS if any dedicated registration regresses or the
// command seam stops wiring one of them into the root.
func TestCommandProviders_ExtractedLeafCommands(t *testing.T) {
	cases := []struct {
		word     string   // Reserved() + top-level command name
		parse    []string // argv selecting a leaf subcommand
		selected string   // expected ctx.Command() after parse
	}{
		{"alias", []string{"alias", "list"}, "alias list"},
		{"ssh", []string{"ssh", "tunnel", "spice", "myvm"}, "ssh tunnel spice <vm>"},
		// `mcp`, `secrets`, `udev`, `tmux`, `preempt`, and `feature` are intentionally absent:
		// `charly mcp serve` (C1), `charly secrets …` (C2), `charly udev …`, `charly tmux …` (the
		// first welded-command externalization), `charly preempt …` (the second), and
		// `charly feature …` (the third) are now EXTERNAL commands served out-of-process by
		// candy/plugin-mcp / candy/plugin-secrets / candy/plugin-udev / candy/plugin-tmux /
		// candy/plugin-preempt / candy/plugin-feature, not builtin CommandProviders.
	}
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

// TestCommandProviders_DeployLifecycleCommands proves every deploy-lifecycle + remaining
// leaf command extracted into a dedicated COMMAND-class provider (the deploy-lifecycle
// batch: start/stop/status/restart/update/remove/logs/shell/cmd/cp/volume/service/config/
// bundle/reap-orphans) is (1) registered in providerRegistry as a CommandProvider with the
// matching Reserved() word, and (2) collected by collectCommandPlugins() and injected into
// the REAL charly CLI grammar via kong.Plugins, so its subcommand path parses and selects
// exactly as before the extraction (the Run handler — which calls the unchanged core
// deploy/bundle machinery — is preserved verbatim). The test FAILS if any dedicated
// registration regresses or the command seam stops wiring one of them into the root.
func TestCommandProviders_DeployLifecycleCommands(t *testing.T) {
	cases := []struct {
		word     string   // Reserved() + top-level command name
		parse    []string // argv selecting the command (or a leaf subcommand)
		selected string   // expected ctx.Command() after parse
	}{
		{"start", []string{"start", "mybox"}, "start <box>"},
		{"stop", []string{"stop", "mybox"}, "stop <box>"},
		{"status", []string{"status", "mybox"}, "status <box>"},
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
		{"bundle", []string{"bundle", "path"}, "bundle path"},
		{"reap-orphans", []string{"reap-orphans"}, "reap-orphans"},
	}
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

// TestCommandProviders_NonMachineryCommands proves the remaining non-machinery commands
// extracted into dedicated COMMAND-class providers — check — are (1)
// registered in providerRegistry as a CommandProvider with the matching Reserved() word,
// and (2) collected by collectCommandPlugins() and injected into the REAL charly CLI
// grammar via kong.Plugins, so each subcommand path parses and selects exactly as before
// the extraction (the Run handlers — the check tree — are preserved verbatim).
// The test FAILS if any dedicated registration regresses or the command seam stops wiring one
// of them into the root. (`feature` and `vm` are no longer here — each is an EXTERNAL command
// served out-of-process by candy/plugin-feature / candy/plugin-vm; vm is the fourth
// welded-command externalization, forwarding to the hidden __vm core command.)
func TestCommandProviders_NonMachineryCommands(t *testing.T) {
	cases := []struct {
		word     string   // Reserved() + top-level command name
		parse    []string // argv selecting a leaf subcommand
		selected string   // expected ctx.Command() after parse
	}{
		{"check", []string{"check", "box", "myimg"}, "check box <image>"},
	}
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

// TestCommandProviders_CheckNestedPluginsInjected proves the coupled half of the check
// extraction: the nested OUT-OF-PROCESS command plugins (NestedCommandProvider with
// CommandParent()=="check" — the shape charly check kube/adb/appium take once externalized)
// still attach UNDER the extracted `check` command. It registers a fake nested-under-check
// provider, collects the builtin command grammar (which now carries `check`), collects the
// external command plugins, and wires them together via attachNestedCheckPlugins exactly as
// main() does — then asserts BOTH that the nested subcommand parses under `check` AND that
// the verbatim built-in check subcommands still parse. The test FAILS if extracting check
// dropped the kong.Plugins nesting seam.
func TestCommandProviders_CheckNestedPluginsInjected(t *testing.T) {
	// Register a fake out-of-process nested-under-check command provider.
	RegisterBuiltinProvider(&fakeNestedCheckCmd{})

	// Collect the builtin command providers (includes the extracted `check`) and the
	// external command plugins (includes the fake nested one), then wire the nested set
	// into the check holder the SAME way main() does.
	cmdPlugins := collectCommandPlugins()
	_, nestedByParent, _ := collectExternalCommandPlugins()
	attachNestedCheckPlugins(cmdPlugins, nestedByParent["check"])

	var cli CLI
	cli.Plugins = cmdPlugins
	parser, err := kong.New(&cli, kong.Name("charly"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatalf("kong.New with the check command + nested plugin: %v", err)
	}

	// 1. The nested external subcommand parses under `check` (kube/adb/appium analogue).
	ctx, err := parser.Parse([]string{"check", "examplekube", "nodes"})
	if err != nil {
		t.Fatalf("nested check subcommand not injected after extracting check: %v", err)
	}
	if got := commandPathKey(ctx.Command()); got != "check examplekube" {
		t.Fatalf("expected nested command \"check examplekube\", got %q", got)
	}

	// 2. The built-in check subcommands still parse (verbatim CheckCmd survived).
	ctx2, err := parser.Parse([]string{"check", "box", "myimg"})
	if err != nil {
		t.Fatalf("built-in check subcommand regressed after extraction: %v", err)
	}
	if got := ctx2.Command(); got != "check box <image>" {
		t.Fatalf("expected \"check box <image>\", got %q", got)
	}
}

// TestCommandProviders_ExtractedReachMCP proves the command extraction did not change the
// reflected CLI surface for the extracted commands — collectCommandPlugins() feeds
// buildCLIModel's modelCLI exactly as it feeds the real CLI, so each extracted command's
// leaves stay in the CLI model the out-of-process MCP bridge (candy/plugin-mcp) reflects into
// tools. `mcp` itself is now an EXTERNAL command served by candy/plugin-mcp — not a builtin
// CommandProvider — so it is correctly ABSENT from this builtin-only model (the MCP server
// does not expose "start an MCP server" as one of its own tools).
func TestCommandProviders_ExtractedReachMCP(t *testing.T) {
	paths := cliModelLeafPaths(t)
	for _, name := range []string{"alias.list"} {
		if !paths[name] {
			t.Errorf("%s missing from the CLI model after extracting its command into a CommandProvider", name)
		}
	}
	if paths["mcp.serve"] {
		t.Error("mcp.serve unexpectedly present in the builtin CLI model — `mcp` is now an external command (candy/plugin-mcp), not a builtin CommandProvider")
	}
	if paths["secrets.list"] {
		t.Error("secrets.list unexpectedly present in the builtin CLI model — `secrets` is now an external command (candy/plugin-secrets), not a builtin CommandProvider")
	}
	if paths["tmux.list"] {
		t.Error("tmux.list unexpectedly present in the builtin CLI model — `tmux` is now an external command (candy/plugin-tmux, the first welded-command externalization), not a builtin CommandProvider")
	}
	if paths["preempt.status"] {
		t.Error("preempt.status unexpectedly present in the builtin CLI model — `preempt` is now an external command (candy/plugin-preempt, the second welded-command externalization), not a builtin CommandProvider")
	}
	if paths["feature.list"] {
		t.Error("feature.list unexpectedly present in the builtin CLI model — `feature` is now an external command (candy/plugin-feature, the third welded-command externalization), not a builtin CommandProvider")
	}
	// C15's remaining welded-command externalizations: candy is an EXTERNAL command
	// (candy/plugin-candy) re-homed onto the hidden __candy core command; clean + settings are now
	// COMPILED-IN and OWN their commands (candy/plugin-clean + candy/plugin-settings, reaching their
	// shared core engine over a generic HostBuild seam — no hidden core command). All are absent from
	// this builtin-only model — a compiled-in command is a DYNAMIC holder (collectExternalCommandPlugins),
	// never a builtin CommandProvider, and the forwarder's hidden __* twin (candy) is marked hidden.
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
		t.Error("candy.set unexpectedly present in the builtin CLI model — `candy` is now an external command (candy/plugin-candy, C15), forwarding to the hidden __candy core command tree")
	}
}
