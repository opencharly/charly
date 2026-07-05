package main

import (
	"context"
	"strings"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/opencharly/sdk"
)

// TestExternalCommandExecPlan_PassthroughArgs proves the external-command FORK/EXEC path: a
// dynamic Kong subcommand built by externalCommandHolder parses a command line, and
// externalCommandExecPlan reads the parsed pass-through args (flags included, via passthrough),
// resolves the plugin binary by word (a baked binary here), and builds the exec argv + env —
// the plan dispatchExternalCommand then hands to syscall.Exec. The env must STRIP the go-plugin
// handshake cookie (so the plugin runs in CLI mode, not serve mode) and stamp CHARLY_BIN.
func TestExternalCommandExecPlan_PassthroughArgs(t *testing.T) {
	const word = "zzexeccmd"
	// Make the strip non-trivial: set the cookie, then assert it is absent from the exec env.
	t.Setenv(sdk.Handshake.MagicCookieKey, sdk.Handshake.MagicCookieValue)
	bakedPluginBinaries[provKey(ClassCommand, word)] = "/fake/plugins/" + word
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field)
	var cli struct{ kong.Plugins }
	cli.Plugins = kong.Plugins{holder}
	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New with dynamic command holder: %v", err)
	}
	if _, err := parser.Parse([]string{word, "nodes", "--wide"}); err != nil {
		t.Fatalf("kong.Parse external command: %v", err)
	}

	d := externalCommandDispatch{word: word, holder: holder, field: field}
	bin, argv, env, err := externalCommandExecPlan(d)
	if err != nil {
		t.Fatalf("externalCommandExecPlan: %v", err)
	}
	if want := "/fake/plugins/" + word; bin != want {
		t.Fatalf("bin = %q, want the baked binary %q", bin, want)
	}
	want := []string{bin, "nodes", "--wide"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full %v)", i, argv[i], want[i], argv)
		}
	}
	assertCommandEnv(t, env)
}

// TestExternalCommandExecPlan_Udev proves the externalized `charly udev` command rides the
// SAME fork/exec seam: a dynamic Kong holder built for the `udev` word parses `udev generate`,
// externalCommandExecPlan resolves the (baked) plugin-udev binary by word and builds the exec
// argv `<bin> generate` + the CLI-mode env (handshake cookie stripped, CHARLY_BIN stamped).
// This is the externalization gate — `charly udev` no longer resolves to a builtin
// CommandProvider; it resolves to candy/plugin-udev over this path.
func TestExternalCommandExecPlan_Udev(t *testing.T) {
	const word = "udev"
	t.Setenv(sdk.Handshake.MagicCookieKey, sdk.Handshake.MagicCookieValue)
	bakedPluginBinaries[provKey(ClassCommand, word)] = "/fake/plugins/plugin-" + word
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field)
	var cli struct{ kong.Plugins }
	cli.Plugins = kong.Plugins{holder}
	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New with the udev command holder: %v", err)
	}
	if _, err := parser.Parse([]string{word, "generate"}); err != nil {
		t.Fatalf("kong.Parse `charly udev generate`: %v", err)
	}

	d := externalCommandDispatch{word: word, holder: holder, field: field}
	bin, argv, env, err := externalCommandExecPlan(d)
	if err != nil {
		t.Fatalf("externalCommandExecPlan: %v", err)
	}
	if want := "/fake/plugins/plugin-" + word; bin != want {
		t.Fatalf("bin = %q, want the baked binary %q", bin, want)
	}
	want := []string{bin, "generate"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full %v)", i, argv[i], want[i], argv)
		}
	}
	assertCommandEnv(t, env)
}

// TestExternalCommandExecPlan_Tmux proves the externalized `charly tmux` command — the FIRST
// welded-command externalization — rides the SAME fork/exec seam: a dynamic Kong holder built
// for the `tmux` word parses `tmux list mybox` (a leaf + box arg), externalCommandExecPlan
// resolves the (baked) plugin-tmux binary by word and builds the exec argv `<bin> list mybox` +
// the CLI-mode env (handshake cookie stripped, CHARLY_BIN stamped). This is the externalization
// gate — `charly tmux` no longer resolves to a builtin CommandProvider; it resolves to
// candy/plugin-tmux over this path, and the plugin re-expresses each leaf as a `charly cmd`/
// `charly shell` shell-back (CHARLY_BIN is the SAME charly that dispatched it).
func TestExternalCommandExecPlan_Tmux(t *testing.T) {
	const word = "tmux"
	t.Setenv(sdk.Handshake.MagicCookieKey, sdk.Handshake.MagicCookieValue)
	bakedPluginBinaries[provKey(ClassCommand, word)] = "/fake/plugins/plugin-" + word
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field)
	var cli struct{ kong.Plugins }
	cli.Plugins = kong.Plugins{holder}
	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New with the tmux command holder: %v", err)
	}
	if _, err := parser.Parse([]string{word, "list", "mybox"}); err != nil {
		t.Fatalf("kong.Parse `charly tmux list mybox`: %v", err)
	}

	d := externalCommandDispatch{word: word, holder: holder, field: field}
	bin, argv, env, err := externalCommandExecPlan(d)
	if err != nil {
		t.Fatalf("externalCommandExecPlan: %v", err)
	}
	if want := "/fake/plugins/plugin-" + word; bin != want {
		t.Fatalf("bin = %q, want the baked binary %q", bin, want)
	}
	want := []string{bin, "list", "mybox"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full %v)", i, argv[i], want[i], argv)
		}
	}
	assertCommandEnv(t, env)
}

// TestExternalCommandExecPlan_Vm proves the externalized `charly vm` command — the FOURTH
// welded-command externalization — rides the SAME fork/exec seam: a dynamic Kong holder built
// for the `vm` word parses `vm list` (a leaf of the VM lifecycle tree), externalCommandExecPlan
// resolves the (baked) plugin-vm binary by word and builds the exec argv `<bin> list` + the
// CLI-mode env (handshake cookie stripped, CHARLY_BIN stamped). This is the externalization
// gate — `charly vm` no longer resolves to a builtin CommandProvider (the deleted in-core
// command provider); it resolves to candy/plugin-vm over this path, and the plugin (command.go)
// raw-forwards the pass-through args to the hidden in-core `charly __vm <args…>` (CHARLY_BIN is
// the SAME charly that dispatched it), so the VmCmd Run handlers run in core with charly's
// inherited stdio/TTY.
func TestExternalCommandExecPlan_Vm(t *testing.T) {
	const word = "vm"
	t.Setenv(sdk.Handshake.MagicCookieKey, sdk.Handshake.MagicCookieValue)
	bakedPluginBinaries[provKey(ClassCommand, word)] = "/fake/plugins/plugin-" + word
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field)
	var cli struct{ kong.Plugins }
	cli.Plugins = kong.Plugins{holder}
	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New with the vm command holder: %v", err)
	}
	if _, err := parser.Parse([]string{word, "list"}); err != nil {
		t.Fatalf("kong.Parse `charly vm list`: %v", err)
	}

	d := externalCommandDispatch{word: word, holder: holder, field: field}
	bin, argv, env, err := externalCommandExecPlan(d)
	if err != nil {
		t.Fatalf("externalCommandExecPlan: %v", err)
	}
	if want := "/fake/plugins/plugin-" + word; bin != want {
		t.Fatalf("bin = %q, want the baked binary %q", bin, want)
	}
	want := []string{bin, "list"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full %v)", i, argv[i], want[i], argv)
		}
	}
	assertCommandEnv(t, env)
}

// TestExternalCommandExecPlan_NestedCheckCommand proves a NestedCommandProvider's dynamic
// command nests UNDER `check` (kong.Plugins embedded in a CheckCmd-like parent), parses
// `check examplekube …`, keys the dispatch table by the full path "check examplekube"
// (commandPathKey), and builds the exec plan from the resolved (baked) binary + pass-through
// args.
func TestExternalCommandExecPlan_NestedCheckCommand(t *testing.T) {
	const word = "zzexecnested"
	bakedPluginBinaries[provKey(ClassCommand, word)] = "/fake/plugins/" + word
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field)

	type checkLike struct {
		Box struct {
			X bool
		} `cmd:"" help:"static sibling"`
		kong.Plugins
	}
	var cli struct {
		Check checkLike `cmd:""`
	}
	cli.Check.Plugins = kong.Plugins{holder}

	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New nested: %v", err)
	}
	kctx, err := parser.Parse([]string{"check", word, "nodes", "--wide"})
	if err != nil {
		t.Fatalf("kong.Parse nested: %v", err)
	}
	if key := commandPathKey(kctx.Command()); key != "check "+word {
		t.Fatalf("commandPathKey(%q) = %q, want %q", kctx.Command(), key, "check "+word)
	}
	d := externalCommandDispatch{word: word, holder: holder, field: field}
	_, argv, _, err := externalCommandExecPlan(d)
	if err != nil {
		t.Fatalf("externalCommandExecPlan: %v", err)
	}
	if len(argv) != 3 || argv[0] != "/fake/plugins/"+word || argv[1] != "nodes" || argv[2] != "--wide" {
		t.Fatalf("argv = %v, want [/fake/plugins/%s nodes --wide]", argv, word)
	}
}

// assertCommandEnv checks commandExecEnv stripped the go-plugin handshake cookie (so the
// fork/exec'd plugin runs in CLI mode, not serve mode — sdk.IsServeMode) and stamped CHARLY_BIN.
func assertCommandEnv(t *testing.T, env []string) {
	t.Helper()
	cookie := sdk.Handshake.MagicCookieKey + "="
	hasBin := false
	for _, e := range env {
		if strings.HasPrefix(e, cookie) {
			t.Fatalf("env must NOT carry the go-plugin handshake cookie %q (the plugin would enter serve mode): %q", cookie, e)
		}
		if strings.HasPrefix(e, "CHARLY_BIN=") {
			hasBin = true
		}
	}
	if !hasBin {
		t.Fatal("env must stamp CHARLY_BIN so the plugin shells back to the dispatching charly")
	}
}

// fakeNestedCheckCmd is a NestedCommandProvider that nests its command under `check` — the
// shape the dep-shed extractions (charly check kube/adb/appium) take. Used by
// TestCommandProviders_CheckNestedPluginsInjected (provider_command_test.go) to prove the
// nested-under-check grammar seam survives the check-command extraction.
type fakeNestedCheckCmd struct{}

func (*fakeNestedCheckCmd) Reserved() string      { return "examplekube" }
func (*fakeNestedCheckCmd) Class() ProviderClass  { return ClassCommand }
func (*fakeNestedCheckCmd) CommandParent() string { return "check" }
func (*fakeNestedCheckCmd) Invoke(context.Context, *Operation) (*Result, error) {
	return &Result{}, nil
}
