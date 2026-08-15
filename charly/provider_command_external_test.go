package main

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/opencharly/spec/climodel"
	"github.com/opencharly/spec/transport"
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

// (The former TestExternalCommandExecPlan_Vm was removed with the P10 VM-CLI move: `charly vm`
// is now a COMPILED-IN command served by candy/plugin-vm (command:vm), dispatched IN-PROC — it
// no longer rides the external fork/exec seam, so it is not an example word for this suite. The
// generic external exec-plan mechanism stays covered by the passthrough/udev/tmux/nested cases.)

// assertExternalCommandExecPlan proves an externalized top-level command rides the fork/exec
// seam: a dynamic Kong holder built for the word parses the given argv, externalCommandExecPlan
// resolves the baked plugin binary by word and builds the exec argv (binary ++ pass-through
// args) + a CLI-mode env (handshake cookie stripped, CHARLY_BIN stamped — asserted by
// assertCommandEnv). Shared by the passthrough/udev/vm exec-plan tests (R3).
func assertExternalCommandExecPlan(t *testing.T, word, bakedBin string, parse, wantTail []string) {
	t.Helper()
	// Set the go-plugin handshake cookie so the strip is non-trivial (assertCommandEnv checks
	// it is absent from the built exec env — otherwise the plugin would enter serve mode).
	t.Setenv(transport.Handshake.MagicCookieKey, transport.Handshake.MagicCookieValue)
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
	assertCommandEnv(t, env, word)
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
	subs := []climodel.CLISubcommand{
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

// TestExternalCommandHolder_HiddenSubcommand proves F-CLI-NEST hidden-but-reachable at the
// host-grammar level: a DECLARED subcommand with Hidden:true (e.g. check's run-local — the
// iterate harness's `charly check run-local <name> --run-id <id>` re-exec) is still a REAL Kong
// `cmd:""` child, so it PARSES and resolves to its dispatch entry — but nestedSubcommandType tags
// it `hidden:""`, so Kong marks the node Hidden and `--help` (and the CLI model, via
// clireflect.KongLeafToCLILeaf's leaf.Hidden check) keep it invisible, byte-identical to the
// `hidden:""` tag on the plugin's own grammar field.
func TestExternalCommandHolder_HiddenSubcommand(t *testing.T) {
	const word = "zzexechidden"
	subs := []climodel.CLISubcommand{
		{Name: "visible", Help: "shown in help"},
		{Name: "run-local", Help: "hidden machinery", Hidden: true},
	}
	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field, subs)

	var cli struct{ kong.Plugins }
	cli.Plugins = kong.Plugins{holder}
	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New with nested command holder for %q: %v", word, err)
	}
	// The hidden child DISPATCHES — the whole point of hidden-but-reachable.
	kctx, err := parser.Parse([]string{word, "run-local", "mybed", "--run-id", "42"})
	if err != nil {
		t.Fatalf("kong.Parse of hidden subcommand: %v", err)
	}
	if got, want := kctx.Command(), word+" run-local <args>"; got != want {
		t.Fatalf("kctx.Command() = %q, want %q", got, want)
	}
	table := map[string]externalCommandDispatch{
		word: {word: word, holder: holder, field: field, subcommands: subs},
	}
	d, sub, ok := resolveCommandDispatch(kctx.Command(), table)
	if !ok {
		t.Fatalf("resolveCommandDispatch(%q) did not resolve", kctx.Command())
	}
	if sub != "run-local" {
		t.Fatalf("resolveCommandDispatch sub = %q, want %q", sub, "run-local")
	}
	if got, want := externalCommandArgs(d, sub), []string{"run-local", "mybed", "--run-id", "42"}; !equalStrings(got, want) {
		t.Fatalf("externalCommandArgs(d, %q) = %v, want %v", sub, got, want)
	}
	// Yet the generated `hidden:""` tag marks the node Hidden (kong build.go: tag.Hidden →
	// node.Hidden), so `--help` and the CLI model keep it invisible; the visible sibling is not.
	byName := map[string]*kong.Node{}
	for _, child := range parser.Model.Children[0].Children {
		byName[child.Name] = child
	}
	visible, ok := byName["visible"]
	if !ok || visible.Hidden {
		t.Fatalf("visible child missing or flagged hidden: %+v", byName)
	}
	hidden, ok := byName["run-local"]
	if !ok {
		t.Fatal("run-local child missing — hidden-but-reachable must still render a Kong cmd node")
	}
	if !hidden.Hidden {
		t.Fatal("run-local child not flagged Hidden — nestedSubcommandType must emit `hidden:\"\"` for a Hidden declared subcommand")
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
// fork/exec'd plugin runs in CLI mode, not serve mode — transport.IsServeMode) and stamped CHARLY_BIN.
func assertCommandEnv(t *testing.T, env []string, word string) {
	t.Helper()
	cookie := transport.Handshake.MagicCookieKey + "="
	hasBin := false
	hasWord := false
	for _, e := range env {
		if strings.HasPrefix(e, cookie) {
			t.Fatalf("env must NOT carry the go-plugin handshake cookie %q (the plugin would enter serve mode): %q", cookie, e)
		}
		if strings.HasPrefix(e, "CHARLY_BIN=") {
			hasBin = true
		}
		if e == "CHARLY_COMMAND_WORD="+word {
			hasWord = true
		}
	}
	if !hasBin {
		t.Fatal("env must stamp CHARLY_BIN so the plugin shells back to the dispatching charly")
	}
	if !hasWord {
		t.Fatalf("env must stamp CHARLY_COMMAND_WORD=%s so a multi-command plugin selects the dispatched grammar", word)
	}
}

// --- the `charly box feature run` unreachability cutover (charly#B) ---

// fakeCommandProvider / fakeNestedCommandProvider are minimal ClassCommand Providers for the
// registry parking + parented-resolve tests. The nested one additionally implements
// NestedCommandProvider, which is exactly what register keys its parking decision on.
type fakeCommandProvider struct{ word string }

func (p *fakeCommandProvider) Reserved() string     { return p.word }
func (p *fakeCommandProvider) Class() ProviderClass { return ClassCommand }
func (p *fakeCommandProvider) Invoke(context.Context, *Operation) (*Result, error) {
	return &Result{}, nil
}

type fakeNestedCommandProvider struct {
	fakeCommandProvider
	parent string
}

func (p *fakeNestedCommandProvider) CommandParent() string { return p.parent }

// TestExportedCommandField_AlwaysExported is the regression gate for the STARTUP PANIC half of the
// cutover. exportedCommandField's contract is an EXPORTED Go field name, and reflect.StructOf
// PANICS on an unexported one — inside collectExternalCommandPlugins, which runs during main(). So
// a single declared subcommand name that broke the contract did not degrade one command, it
// aborted the whole binary before any command could run.
//
// Capitalizing the first byte only works for a leading LETTER: `__feature-box` (the hidden bridge
// leaf `charly box feature run` forwards) sanitizes to `__feature_box`, whose leading `_` ToUpper
// leaves alone. Fails without the fix — not with an assertion, but by panicking in the
// nestedSubcommandType arm below, exactly as the real binary did.
func TestExportedCommandField_AlwaysExported(t *testing.T) {
	cases := []string{"__feature-box", "_leading-underscore", "9lives", "feature-box", "box", "x"}
	for _, word := range cases {
		got := exportedCommandField(word)
		if got == "" {
			t.Fatalf("exportedCommandField(%q) = empty", word)
		}
		if c := got[0]; c < 'A' || c > 'Z' {
			t.Errorf("exportedCommandField(%q) = %q — first byte %q is not an uppercase letter, so reflect.StructOf rejects the field as unexported", word, got, string(c))
		}
	}
	// The property that actually matters: reflect.StructOf must ACCEPT the derived name. This is
	// the call that panicked the binary at startup.
	subs := []climodel.CLISubcommand{{Name: "__feature-box", Help: "hidden bridge leaf", Hidden: true}}
	_ = nestedSubcommandType(subs) // panics on the pre-fix derivation
}

// TestResolveCommand_NestedWinsOverCollidingTopLevel is the regression gate for the DISPATCH half.
// The registry deliberately gives the plain key to a TOP-LEVEL command and parks a colliding NESTED
// one at "command:<word>:<parent>" (register's parking rule). dispatchCommand resolved by the plain
// word alone, so `charly box feature run` was handed the TOP-LEVEL `charly feature` capability,
// whose grammar has no `run` — the nested command was unreachable from the day it was introduced.
//
// Fails without resolveCommand: the parented lookup returns the top-level provider.
func TestResolveCommand_NestedWinsOverCollidingTopLevel(t *testing.T) {
	r := newRegistry()
	top := &fakeCommandProvider{word: "zzfeature"}
	nested := &fakeNestedCommandProvider{fakeCommandProvider: fakeCommandProvider{word: "zzfeature"}, parent: "zzbox"}
	if err := r.register(top, "test-top"); err != nil {
		t.Fatalf("register top-level: %v", err)
	}
	if err := r.register(nested, "test-nested"); err != nil {
		t.Fatalf("register nested: %v", err)
	}

	// A NESTED invocation must reach the nested capability.
	got, ok := r.resolveCommand("zzfeature", "zzbox")
	if !ok {
		t.Fatal("resolveCommand(zzfeature, zzbox) did not resolve")
	}
	if got != Provider(nested) {
		t.Fatalf("resolveCommand(zzfeature, zzbox) returned the TOP-LEVEL provider — `charly zzbox zzfeature` would run `charly zzfeature`")
	}
	// A TOP-LEVEL invocation is unchanged.
	got, ok = r.resolveCommand("zzfeature", "")
	if !ok || got != Provider(top) {
		t.Fatalf("resolveCommand(zzfeature, \"\") = %v, ok=%v — want the top-level provider", got, ok)
	}
	// A nested word that never COLLIDED was never parked: the parented key misses and the plain key
	// answers. This is every other `box <word>` (build, validate, list, …), each uniquely worded —
	// the reason only `feature` was broken.
	lone := &fakeNestedCommandProvider{fakeCommandProvider: fakeCommandProvider{word: "zzlonely"}, parent: "zzbox"}
	if err := r.register(lone, "test-lone"); err != nil {
		t.Fatalf("register lone nested: %v", err)
	}
	if got, ok := r.resolveCommand("zzlonely", "zzbox"); !ok || got != Provider(lone) {
		t.Fatalf("resolveCommand(zzlonely, zzbox) = %v, ok=%v — want the uniquely-worded nested provider via the plain key", got, ok)
	}
}

// TestCollectExternalCommandPlugins_NestedCarriesParent proves the wiring between the two halves:
// the dispatch entry collectExternalCommandPlugins BUILDS for a NESTED command must carry that
// command's parent, or dispatchCommand has nothing to resolve the parented registry key with and
// falls back to the plain word — the top-level capability, which is the bug this cutover fixes.
//
// It drives the REAL collector over a real Registry (the package-level one, swapped for the
// duration) rather than hand-building the struct: the single assignment under test lives inside
// collectExternalCommandPlugins, so a test that constructs externalCommandDispatch itself asserts
// only that the FIELD exists and stays green when the assignment is deleted.
func TestCollectExternalCommandPlugins_NestedCarriesParent(t *testing.T) {
	nested := &fakeNestedCommandProvider{
		fakeCommandProvider: fakeCommandProvider{word: "zzfeature"},
		parent:              "zzbox",
	}
	r := newRegistry()
	if err := r.register(nested, "test-nested"); err != nil {
		t.Fatalf("register nested: %v", err)
	}
	saved := providerRegistry
	providerRegistry = r
	defer func() { providerRegistry = saved }()

	_, nestedByParent, table := collectExternalCommandPlugins()

	// Grammar half: the holder attaches under the parent command, not at the CLI root.
	if n := len(nestedByParent["zzbox"]); n != 1 {
		t.Fatalf("nestedByParent[zzbox] has %d holders, want 1 — the nested grammar was not built", n)
	}
	// Dispatch half: the entry is keyed by the nested PATH and records the parent.
	d, ok := table["zzbox zzfeature"]
	if !ok {
		t.Fatalf("dispatch table has no %q entry; keys = %v", "zzbox zzfeature", tableKeys(table))
	}
	if d.parent != "zzbox" {
		t.Fatalf("collectExternalCommandPlugins recorded parent = %q, want %q — dispatchCommand would resolve the plain word and reach the TOP-LEVEL capability", d.parent, "zzbox")
	}
	// End to end through the lookup dispatchCommand actually performs.
	got, sub, ok := resolveCommandDispatch("zzbox zzfeature <args>", table)
	if !ok || sub != "" {
		t.Fatalf("resolveCommandDispatch(zzbox zzfeature) ok=%v sub=%q", ok, sub)
	}
	if got.parent != "zzbox" {
		t.Fatalf("resolved dispatch parent = %q, want %q", got.parent, "zzbox")
	}
}

// tableKeys lists a dispatch table's keys for a failure message.
func tableKeys(table map[string]externalCommandDispatch) []string {
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
