package main

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"

	"github.com/alecthomas/kong"

	"github.com/opencharly/sdk"
)

// externalCommandDispatch pairs an OUT-OF-PROCESS command word with the dynamic Kong holder
// struct kong.Parse fills, so the parsed pass-through args can be read and forwarded to the
// plugin AFTER parsing. Built by collectExternalCommandPlugins; consulted in main once
// kong.Parse has run. A command is dispatched by RESOLVING its plugin binary by word and
// syscall.Exec'ing it (dispatchExternalCommand) — charly becomes the plugin, which then
// inherits charly's terminal stdio/TTY natively (the whole point of the fork/exec seam).
type externalCommandDispatch struct {
	word string // the command word: keys the binary resolve
	// holder is *struct{ <field> *struct{ Args []string } } (the flat pass-through shape) when
	// subcommands is empty, or *struct{ <field> *struct{ <Child1> *struct{Args []string} `cmd:""...`; ... } }
	// (F-CLI-NEST — one level of DECLARED subcommands, each still ending in a pass-through leaf)
	// when it isn't.
	holder      any
	field       string              // the exported holder field name (Kong needs exported fields)
	subcommands []sdk.CLISubcommand // non-empty => the NESTED holder shape; empty => the flat shape
}

// collectExternalCommandPlugins builds a dynamic Kong subcommand for every OUT-OF-PROCESS
// command — a ClassCommand that is NOT a builtin CommandProvider (the builtin ones contribute
// a static KongCommand()). reflect.StructOf cannot attach methods, so a dynamic command has no
// Run() handler and Kong's ctx.Run() cannot dispatch it; these are dispatched MANUALLY
// post-parse via dispatchExternalCommand. Returns the holder structs for kong.Plugins embedding
// — TOP-LEVEL on the CLI root, or NESTED under a parent command (e.g. `check`) for a provider
// implementing NestedCommandProvider — plus the dispatch table keyed by the command's OWN
// registered PATH ("vm" top-level, "box generate" nested) — NOT the full Kong-rendered path a
// declared subcommand may extend by one more token; resolveCommandDispatch (main.go) is what
// strips that extra token back off at lookup time. TWO sources, unioned:
//   - (a) an already-CONNECTED external command provider in the registry (the eager path —
//     uncommon, since command plugins are never connected for dispatch; carries the
//     NestedCommandProvider parent for grammar nesting AND the commandSubcommandCarrier catalog);
//   - (b) a PRESCANNED command word (prescanProjectCommandWords, run in main before kong.Parse)
//     — the common path: a TOP-LEVEL grammar holder with no declared subcommands (no Describe has
//     run yet to learn any).
//
// Empty (no external commands) when the project declares no command plugins — the grammar is
// then byte-for-byte the builtin set.
func collectExternalCommandPlugins() (topLevel kong.Plugins, nestedByParent map[string]kong.Plugins, table map[string]externalCommandDispatch) {
	nestedByParent = map[string]kong.Plugins{}
	table = map[string]externalCommandDispatch{}
	seen := map[string]bool{} // command words already given a holder
	// (a) already-registered external command providers (rare at dispatch time, but the
	// NestedCommandProvider parent info is needed for grammar nesting).
	for _, p := range providerRegistry.allProviders() {
		if p.Class() != ClassCommand {
			continue
		}
		if _, builtin := p.(CommandProvider); builtin {
			continue // builtin commands use their static, compiled-in KongCommand()
		}
		word := p.Reserved()
		field := exportedCommandField(word)
		var subs []sdk.CLISubcommand
		if sc, ok := p.(commandSubcommandCarrier); ok {
			subs = sc.declaredSubcommands()
		}
		holder := externalCommandHolder(word, field, subs)
		d := externalCommandDispatch{word: word, holder: holder, field: field, subcommands: subs}
		seen[word] = true
		if ncp, ok := p.(NestedCommandProvider); ok {
			if parent := ncp.CommandParent(); parent != "" {
				nestedByParent[parent] = append(nestedByParent[parent], holder)
				table[parent+" "+word] = d
				continue
			}
		}
		topLevel = append(topLevel, holder)
		table[word] = d
	}
	// (b) prescanned command words — TOP-LEVEL holders, no declared subcommands (no Describe has
	// run yet). Nested external commands stay the registered path (a): the prescan learns only
	// the word, not its parent, and no real nested external command exists today.
	for _, word := range declaredExternalCommandWords() {
		if seen[word] {
			continue
		}
		field := exportedCommandField(word)
		holder := externalCommandHolder(word, field, nil)
		topLevel = append(topLevel, holder)
		table[word] = externalCommandDispatch{word: word, holder: holder, field: field}
	}
	return topLevel, nestedByParent, table
}

// NestedCommandProvider is an optional refinement of a ClassCommand Provider: it nests its
// command UNDER an existing parent command (e.g. `check`) rather than at the CLI root. The
// parent command must embed kong.Plugins for the dynamic subcommand to attach (BoxCmd
// does). Used by the compiled-in command:box plugin —
// `charly box generate`/`new`/`validate`/`pkg` nested under `box` (candy/plugin-box); the check
// verbs kube/adb/appium stay `verb:` providers, a possible future nested-command target.
type NestedCommandProvider interface {
	Provider
	CommandParent() string
}

// commandSubcommandCarrier is an optional refinement of a ClassCommand Provider (F-CLI-NEST): it
// DECLARES its own one-level-deep CLI subcommand catalog (a name+help pair per child), advertised
// over Describe via ProvidedCapability.Subcommands. collectExternalCommandPlugins uses it to build
// a REAL nested Kong grammar — a named `cmd:""` child per entry, each still ending in a pass-through
// Args leaf — in place of the opaque `[<args>...]` holder every OTHER command-class capability
// gets, restoring both `--help` fidelity and `charly __cli-model` (MCP) leaf discoverability for
// the declared children. Promoted onto both provider placements via capMeta embedding; a capability
// that declares no subcommands (or a prescanned-but-unconnected external command, before any
// Describe has run) keeps today's flat holder unchanged.
type commandSubcommandCarrier interface {
	Provider
	declaredSubcommands() []sdk.CLISubcommand
}

// commandPathKey strips the trailing " <args>" placeholder Kong renders for a command's
// deepest pass-through Args leaf, yielding the command PATH Kong actually parsed:
// "examplecmd <args>" → "examplecmd"; "check kube <args>" → "check kube";
// "check live <args>" → "check live" (F-CLI-NEST: "live" is a DECLARED subcommand of "check", one
// token deeper than the "check" entry collectExternalCommandPlugins registered in the dispatch
// table — resolveCommandDispatch is what strips that extra token back off).
func commandPathKey(kongCommand string) string {
	return strings.TrimSuffix(kongCommand, " <args>")
}

// resolveCommandDispatch resolves a Kong-rendered command path (via ctx.Command(), e.g.
// "check <args>", "check live <args>", "box list <args>", or "box list boxes <args>") to its
// registered externalCommandDispatch entry plus the declared-subcommand NAME (if any) that must be
// PREPENDED to the args forwarded to the plugin. A subcommand catalog (F-CLI-NEST) is declared
// exactly ONE level deep, so at most one extra path token can separate the capability's own
// registered table key from the fully-rendered path: try the path as-is first (the flat holder, or
// a CommandParent-nested command with no further declared subcommands — unchanged behavior); if
// that misses, drop the LAST token (the selected child's name) and retry — a hit there means the
// dropped token is the child to prepend.
func resolveCommandDispatch(kongCommand string, table map[string]externalCommandDispatch) (externalCommandDispatch, string, bool) {
	path := commandPathKey(kongCommand)
	if d, ok := table[path]; ok {
		return d, "", true
	}
	if idx := strings.LastIndex(path, " "); idx >= 0 {
		if d, ok := table[path[:idx]]; ok {
			return d, path[idx+1:], true
		}
	}
	return externalCommandDispatch{}, "", false
}

// externalCommandHolder builds a Kong command holder for one out-of-process (or compiled-in
// dynamic-dispatch) command:
//
//	*struct{ <Field> *struct{ Args []string `arg:"" passthrough:""` } `cmd:"" name:"<word>"` }
//
// When subcommands is non-empty (F-CLI-NEST), the inner struct is instead a NAMED child per
// declared entry — each STILL ending in its own pass-through Args leaf, so the plugin's real
// internal flag/positional shape beneath any one child stays invisible to the host exactly like
// today's flat holder; only the CHILD NAMING becomes real, which is what makes it a genuine Kong
// `cmd:""` node (shows in `--help`, walkable by `charly __cli-model`) instead of an opaque
// passthrough. The plugin parses its own flags beyond that (its CLI grammar owns that contract),
// so the core needs no per-flag knowledge here either way.
func externalCommandHolder(word, field string, subcommands []sdk.CLISubcommand) any {
	bodyType := passthroughArgsType()
	if len(subcommands) > 0 {
		bodyType = nestedSubcommandType(subcommands)
	}
	holderType := reflect.StructOf([]reflect.StructField{
		{
			Name: field,
			Type: reflect.PointerTo(bodyType),
			Tag:  reflect.StructTag(fmt.Sprintf(`cmd:"" name:%q help:%q`, word, word+" (out-of-process command plugin)")),
		},
	})
	return reflect.New(holderType).Interface()
}

// passthroughArgsType is the pass-through Args leaf every command holder bottoms out in — the flat
// holder's own body, or one declared subcommand's body under the F-CLI-NEST nested shape.
func passthroughArgsType() reflect.Type {
	return reflect.StructOf([]reflect.StructField{
		{
			Name: "Args",
			Type: reflect.TypeOf([]string{}),
			Tag:  `arg:"" optional:"" passthrough:"" help:"arguments forwarded to the command plugin"`,
		},
	})
}

// nestedSubcommandType builds the F-CLI-NEST inner struct: one named `cmd:""` field per declared
// subcommand, each a pointer to its own pass-through Args leaf.
func nestedSubcommandType(subcommands []sdk.CLISubcommand) reflect.Type {
	fields := make([]reflect.StructField, 0, len(subcommands))
	for _, sc := range subcommands {
		fields = append(fields, reflect.StructField{
			Name: exportedCommandField(sc.Name),
			Type: reflect.PointerTo(passthroughArgsType()),
			Tag:  reflect.StructTag(fmt.Sprintf(`cmd:"" name:%q help:%q`, sc.Name, sc.Help)),
		})
	}
	return reflect.StructOf(fields)
}

// dispatchCommand routes a parsed dynamic command to its provider by PLACEMENT (F8): a
// COMPILED-IN command candy (registered in-proc as an inprocProvider — NOT a *grpcProvider and
// NOT a static builtin CommandProvider) dispatches IN-PROC via Invoke(OpRun), so the candy's
// handler runs inside charly's own process with native stdio/TTY; an OUT-OF-PROCESS command
// dispatches by syscall.Exec'ing its plugin binary (dispatchExternalCommand). This is the command
// half of placement-invisibility: the SAME command candy works compiled-in or out-of-process,
// the dynamic Kong grammar (externalCommandHolder) identical for both — only the dispatch
// transport differs. The dynamic grammar carries no Run() method, so dispatch is manual either way.
// sub is the declared-subcommand NAME resolveCommandDispatch recovered ("" for the flat case, or a
// CommandParent nesting with no further declared subcommands).
func dispatchCommand(d externalCommandDispatch, sub string) error {
	if prov, ok := providerRegistry.resolve(ClassCommand, d.word); ok {
		if _, external := prov.(*grpcProvider); !external {
			return dispatchInProcCommand(prov, d, sub)
		}
	}
	return dispatchExternalCommand(d, sub)
}

// dispatchInProcCommand forwards a compiled-in command's parsed pass-through args to its in-proc
// provider via Invoke(OpRun) — the candy's OpRun handler runs in charly's process (it owns
// os.Stdout/Stderr/TTY natively), mirroring the OUT-OF-PROCESS plugin's pass-through `{"args":[…]}`
// envelope (the OpRun contract), so a command candy behaves identically in either placement.
func dispatchInProcCommand(prov Provider, d externalCommandDispatch, sub string) error {
	params, err := marshalJSON(map[string]any{"args": externalCommandArgs(d, sub)})
	if err != nil {
		return fmt.Errorf("command %q: marshal args: %w", d.word, err)
	}
	// Thread the in-proc reverse channel so a compiled-in command's Invoke(OpRun) can call back
	// HostBuild / InvokeProvider (the command-class analogue of how the build class gets its in-proc
	// reverse channel — build.go dispatchBuild). Generic: every compiled-in command benefits, so a
	// command plugin can OWN its logic and reach the shared host machinery (e.g. clean's "retention"
	// HostBuild) instead of forwarding the whole command to a hidden `__<cmd>` core handler. The
	// executor carries no venue — a command's HostBuild legs reconstruct their engine host-side.
	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	if _, err := prov.Invoke(ctx, &Operation{Reserved: d.word, Op: sdk.OpRun, Params: params}); err != nil {
		return fmt.Errorf("command %q: %w", d.word, err)
	}
	return nil
}

// dispatchExternalCommand resolves the command word's plugin binary (baked into the image, or
// host-built from the candy source — the SAME resolver the loader uses) and REPLACES the charly
// process with it via syscall.Exec, forwarding the parsed pass-through args. The plugin runs in
// CLI mode (the handshake cookie is stripped from the env, so it does not enter go-plugin serve
// mode) and inherits charly's stdin/stdout/stderr/TTY natively — so `charly mcp serve --stdio`
// reaches a real terminal again, every external command gets real stdout/$EDITOR, and the
// plugin BECOMES the process (a deployed `--listen` service has no wrapper and no
// signal-forwarding hop). On success this never returns; only a PRE-exec failure (binary
// missing / build fail / bad env) returns an error.
func dispatchExternalCommand(d externalCommandDispatch, sub string) error {
	bin, argv, env, err := externalCommandExecPlan(d, sub)
	if err != nil {
		return err
	}
	if err := syscall.Exec(bin, argv, env); err != nil {
		return fmt.Errorf("command %q: exec %s: %w", d.word, bin, err)
	}
	return nil // unreachable: syscall.Exec replaced the process image
}

// externalCommandExecPlan is the testable half of dispatchExternalCommand (the syscall.Exec
// itself replaces the process image and cannot be unit-tested): it reads the pass-through args
// out of the kong-populated holder, resolves the plugin binary by word, and builds the exec
// argv (binary + args) and env (charly's environ minus the go-plugin handshake cookie, plus
// CHARLY_BIN).
func externalCommandExecPlan(d externalCommandDispatch, sub string) (bin string, argv, env []string, err error) {
	args := externalCommandArgs(d, sub)
	bin, err = resolveCommandPluginBinary(context.Background(), d.word)
	if err != nil {
		return "", nil, nil, err
	}
	argv = append([]string{bin}, args...)
	env = commandExecEnv(d.word)
	return bin, argv, env, nil
}

// externalCommandArgs reads the kong-populated pass-through Args out of the dynamic holder
// struct by reflection. For the flat holder (sub == ""), that's the field's own Args slice. For
// the F-CLI-NEST nested holder, sub names WHICH declared child Kong selected — its Args slice is
// read one level deeper and the child's own NAME is prepended (dispatchCommand/the plugin's own
// internal Kong parse expects "<subcommand> <its args...>", exactly like the flat pass-through
// case forwards "<whatever the user typed>" verbatim). Returns nil when no args were supplied.
func externalCommandArgs(d externalCommandDispatch, sub string) []string {
	cmdField := reflect.ValueOf(d.holder).Elem().FieldByName(d.field)
	if !cmdField.IsValid() || cmdField.IsNil() {
		return nil
	}
	container := cmdField.Elem()
	if sub == "" {
		if a, ok := container.FieldByName("Args").Interface().([]string); ok {
			return a
		}
		return nil
	}
	childField := container.FieldByName(exportedCommandField(sub))
	if !childField.IsValid() || childField.IsNil() {
		return []string{sub}
	}
	var childArgs []string
	if a, ok := childField.Elem().FieldByName("Args").Interface().([]string); ok {
		childArgs = a
	}
	return append([]string{sub}, childArgs...)
}

// resolveCommandPluginBinary returns the provider binary that serves command:<word>. A BAKED
// binary is preferred — a deployed container has no candy source and no go toolchain, so
// discoverBakedPluginWords (run in main) mapped the word to its baked binary from the
// `.providers` manifest. Otherwise the project is scanned for the candy declaring
// command:<word> and its binary is resolved the SAME way the loader does (resolvePluginBinary:
// baked-by-leaf if present, else host-built from source).
func resolveCommandPluginBinary(ctx context.Context, word string) (string, error) {
	if bin, ok := bakedPluginBinaries[provKey(ClassCommand, word)]; ok {
		return bin, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("command %q: resolve cwd: %w", word, err)
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		return "", fmt.Errorf("command %q: load project: %w", word, err)
	}
	candyMap, err := ScanAllCandyWithConfigOpts(dir, cfg, ResolveOpts{})
	if err != nil || candyMap == nil {
		return "", fmt.Errorf("command %q: scan candies: %w", word, err)
	}
	name, candy := findCommandPluginCandy(candyMap, word)
	if candy == nil {
		return "", fmt.Errorf("command %q: no plugin candy provides command:%s in the project", word, word)
	}
	bin, err := resolvePluginBinary(ctx, candy.SourceDir, name)
	if err != nil {
		return "", fmt.Errorf("command %q: %w", word, err)
	}
	return bin, nil
}

// findCommandPluginCandy returns the scanned-set key + candy of the plugin candy whose
// declaration provides command:<word>, or ("", nil) if none does.
func findCommandPluginCandy(candies map[string]*Candy, word string) (string, *Candy) {
	for name, candy := range candies {
		if candy == nil || candy.Plugin == nil {
			continue
		}
		for _, capability := range candy.Plugin.Providers {
			if class, w, ok := splitCapability(string(capability)); ok && class == ClassCommand && w == word {
				return name, candy
			}
		}
	}
	return "", nil
}

// commandExecEnv is charly's process environment with the go-plugin handshake cookie STRIPPED
// (so the fork/exec'd plugin runs in CLI mode, not serve mode — see sdk.IsServeMode) plus
// CHARLY_BIN stamped with charly's own executable, so a command plugin that shells BACK to
// charly (the MCP bridge fork/execs `charly __cli-model` + `charly <cmd>`) calls the SAME
// binary that dispatched it, not whatever `charly` is on PATH — matching LocalTransport.
func commandExecEnv(word string) []string {
	cookie := sdk.Handshake.MagicCookieKey + "="
	src := os.Environ()
	env := make([]string, 0, len(src)+1)
	for _, e := range src {
		if strings.HasPrefix(e, cookie) {
			continue
		}
		env = append(env, e)
	}
	if exe, err := os.Executable(); err == nil {
		env = append(env, "CHARLY_BIN="+exe)
	}
	env = append(env, "CHARLY_COMMAND_WORD="+word)
	return env
}

// exportedCommandField makes an exported (capitalized, alnum-only) Go field name from a
// command word so reflect.StructOf accepts it (Kong requires exported fields); the `name:`
// tag carries the real CLI word, so the field name itself is never user-visible.
func exportedCommandField(word string) string {
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, word)
	if clean == "" {
		return "Cmd"
	}
	return strings.ToUpper(clean[:1]) + clean[1:]
}
