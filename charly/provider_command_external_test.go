package main

import (
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
	assertExternalCommandExecPlan(t, word, "/fake/plugins/"+word,
		[]string{word, "nodes", "--wide"}, []string{"nodes", "--wide"})
}

// TestExternalCommandExecPlan_Udev proves the externalized `charly udev` command rides the
// SAME fork/exec seam: a dynamic Kong holder built for the `udev` word parses `udev generate`,
// externalCommandExecPlan resolves the (baked) plugin-udev binary by word and builds the exec
// argv `<bin> generate` + the CLI-mode env (handshake cookie stripped, CHARLY_BIN stamped).
// This is the externalization gate — `charly udev` no longer resolves to a builtin
// CommandProvider; it resolves to candy/plugin-udev over this path.
func TestExternalCommandExecPlan_Udev(t *testing.T) {
	const word = "udev"
	assertExternalCommandExecPlan(t, word, "/fake/plugins/plugin-"+word,
		[]string{word, "generate"}, []string{"generate"})
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
	assertExternalCommandExecPlan(t, word, "/fake/plugins/plugin-"+word,
		[]string{word, "list", "mybox"}, []string{"list", "mybox"})
}

// (The former TestExternalCommandExecPlan_Vm was removed with the P10 VM-CLI move: `charly vm`
// is now a COMPILED-IN command served by candy/plugin-vm (command:vm), dispatched IN-PROC — it
// no longer rides the external fork/exec seam, so it is not an example word for this suite. The
// generic external exec-plan mechanism stays covered by the passthrough/udev/tmux/nested cases.)

// assertExternalCommandExecPlan proves an externalized top-level command rides the fork/exec
// seam: a dynamic Kong holder built for the word parses the given argv, externalCommandExecPlan
// resolves the baked plugin binary by word and builds the exec argv (binary ++ pass-through
// args) + a CLI-mode env (handshake cookie stripped, CHARLY_BIN stamped — asserted by
// assertCommandEnv). Shared by the passthrough/udev/tmux/vm exec-plan tests (R3).
func assertExternalCommandExecPlan(t *testing.T, word, bakedBin string, parse, wantTail []string) {
	t.Helper()
	// Set the go-plugin handshake cookie so the strip is non-trivial (assertCommandEnv checks
	// it is absent from the built exec env — otherwise the plugin would enter serve mode).
	t.Setenv(sdk.Handshake.MagicCookieKey, sdk.Handshake.MagicCookieValue)
	bakedPluginBinaries[provKey(ClassCommand, word)] = bakedBin
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field, nil)
	var cli struct{ kong.Plugins }
	cli.Plugins = kong.Plugins{holder}
	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New with dynamic command holder for %q: %v", word, err)
	}
	if _, err := parser.Parse(parse); err != nil {
		t.Fatalf("kong.Parse %v: %v", parse, err)
	}

	d := externalCommandDispatch{word: word, holder: holder, field: field}
	bin, argv, env, err := externalCommandExecPlan(d, "")
	if err != nil {
		t.Fatalf("externalCommandExecPlan: %v", err)
	}
	if bin != bakedBin {
		t.Fatalf("bin = %q, want the baked binary %q", bin, bakedBin)
	}
	want := append([]string{bin}, wantTail...)
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
// command nests UNDER `check` (kong.Plugins embedded in a check-like parent), parses
// `check examplekube …`, keys the dispatch table by the full path "check examplekube"
// (commandPathKey), and builds the exec plan from the resolved (baked) binary + pass-through
// args.
func TestExternalCommandExecPlan_NestedCheckCommand(t *testing.T) {
	const word = "zzexecnested"
	bakedPluginBinaries[provKey(ClassCommand, word)] = "/fake/plugins/" + word
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field, nil)

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
	_, argv, _, err := externalCommandExecPlan(d, "")
	if err != nil {
		t.Fatalf("externalCommandExecPlan: %v", err)
	}
	if len(argv) != 3 || argv[0] != "/fake/plugins/"+word || argv[1] != "nodes" || argv[2] != "--wide" {
		t.Fatalf("argv = %v, want [/fake/plugins/%s nodes --wide]", argv, word)
	}
}

// TestExternalCommandHolder_DeclaredSubcommands proves the F-CLI-NEST nested holder shape: a
// command declaring a subcommand catalog gets a REAL named `cmd:""` child per entry (so Kong's own
// `--help` lists them, unlike the opaque flat pass-through), and resolveCommandDispatch +
// externalCommandArgs correctly recover which child was selected plus its own forwarded args —
// restoring both `--help` fidelity and CLI-model (MCP) leaf discoverability for a plugin that
// declares one (candy/plugin-check, candy/plugin-box's "list" word).
func TestExternalCommandHolder_DeclaredSubcommands(t *testing.T) {
	const word = "zzexecdeclared"
	subs := []sdk.CLISubcommand{
		{Name: "live", Help: "run the live check"},
		{Name: "box", Help: "run the box check"},
	}
	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field, subs)

	var cli struct{ kong.Plugins }
	cli.Plugins = kong.Plugins{holder}
	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New with nested command holder for %q: %v", word, err)
	}
	kctx, err := parser.Parse([]string{word, "live", "mydeploy"})
	if err != nil {
		t.Fatalf("kong.Parse: %v", err)
	}
	// Kong renders the DECLARED child as a real subcommand node — one token deeper than the
	// registered table key (just the word itself).
	if got, want := kctx.Command(), word+" live <args>"; got != want {
		t.Fatalf("kctx.Command() = %q, want %q", got, want)
	}

	table := map[string]externalCommandDispatch{
		word: {word: word, holder: holder, field: field, subcommands: subs},
	}
	d, sub, ok := resolveCommandDispatch(kctx.Command(), table)
	if !ok {
		t.Fatalf("resolveCommandDispatch(%q) did not resolve", kctx.Command())
	}
	if sub != "live" {
		t.Fatalf("resolveCommandDispatch sub = %q, want %q", sub, "live")
	}
	if got, want := externalCommandArgs(d, sub), []string{"live", "mydeploy"}; !equalStrings(got, want) {
		t.Fatalf("externalCommandArgs(d, %q) = %v, want %v", sub, got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
