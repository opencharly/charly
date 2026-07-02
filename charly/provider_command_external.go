package main

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"

	"github.com/alecthomas/kong"

	"github.com/opencharly/charly/charly/plugin/sdk"
)

// externalCommandDispatch pairs an OUT-OF-PROCESS command word with the dynamic Kong holder
// struct kong.Parse fills, so the parsed pass-through args can be read and forwarded to the
// plugin AFTER parsing. Built by collectExternalCommandPlugins; consulted in main once
// kong.Parse has run. A command is dispatched by RESOLVING its plugin binary by word and
// syscall.Exec'ing it (dispatchExternalCommand) — charly becomes the plugin, which then
// inherits charly's terminal stdio/TTY natively (the whole point of the fork/exec seam).
type externalCommandDispatch struct {
	word   string // the command word: keys the binary resolve
	holder any    // *struct{ <field> *struct{ Args []string } }
	field  string // the exported holder field name (Kong needs exported fields)
}

// collectExternalCommandPlugins builds a dynamic Kong subcommand for every OUT-OF-PROCESS
// command — a ClassCommand that is NOT a builtin CommandProvider (the builtin ones contribute
// a static KongCommand()). reflect.StructOf cannot attach methods, so a dynamic command has no
// Run() handler and Kong's ctx.Run() cannot dispatch it; these are dispatched MANUALLY
// post-parse via dispatchExternalCommand. Returns the holder structs for kong.Plugins embedding
// — TOP-LEVEL on the CLI root, or NESTED under a parent command (e.g. `check`) for a provider
// implementing NestedCommandProvider — plus the dispatch table keyed by the full command PATH
// ("vm" top-level, "check kube" nested). TWO sources, unioned:
//   - (a) an already-CONNECTED external command provider in the registry (the eager path —
//     uncommon, since command plugins are never connected for dispatch; carries the
//     NestedCommandProvider parent for grammar nesting);
//   - (b) a PRESCANNED command word (prescanProjectCommandWords, run in main before kong.Parse)
//     — the common path: a TOP-LEVEL grammar holder.
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
		holder := externalCommandHolder(word, field)
		d := externalCommandDispatch{word: word, holder: holder, field: field}
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
	// (b) prescanned command words — TOP-LEVEL holders. Nested external commands stay the
	// registered path (a): the prescan learns only the word, not its parent, and no real
	// nested external command exists today.
	for _, word := range declaredExternalCommandWords() {
		if seen[word] {
			continue
		}
		field := exportedCommandField(word)
		holder := externalCommandHolder(word, field)
		topLevel = append(topLevel, holder)
		table[word] = externalCommandDispatch{word: word, holder: holder, field: field}
	}
	return topLevel, nestedByParent, table
}

// NestedCommandProvider is an optional refinement of a ClassCommand Provider: it nests its
// command UNDER an existing parent command (e.g. `check`) rather than at the CLI root. The
// parent command must embed kong.Plugins for the dynamic subcommand to attach (CheckCmd
// does). Used by the dep-shed command extractions — `charly check kube`/`adb`/`appium`.
type NestedCommandProvider interface {
	Provider
	CommandParent() string
}

// commandPathKey strips the trailing " <args>" placeholder Kong renders for an external
// command's pass-through Args, yielding the command PATH that keys the dispatch table:
// "examplecmd <args>" → "examplecmd"; "check kube <args>" → "check kube".
func commandPathKey(kongCommand string) string {
	return strings.TrimSuffix(kongCommand, " <args>")
}

// externalCommandHolder builds a Kong command holder for one out-of-process command:
//
//	*struct{ <Field> *struct{ Args []string `arg:"" passthrough:""` } `cmd:"" name:"<word>"` }
//
// The pass-through Args carry every token after the command word; the plugin parses its own
// flags (its CLI grammar owns that contract), so the core needs no per-flag knowledge here.
func externalCommandHolder(word, field string) any {
	argsType := reflect.StructOf([]reflect.StructField{
		{
			Name: "Args",
			Type: reflect.TypeOf([]string{}),
			Tag:  `arg:"" optional:"" passthrough:"" help:"arguments forwarded to the command plugin"`,
		},
	})
	holderType := reflect.StructOf([]reflect.StructField{
		{
			Name: field,
			Type: reflect.PointerTo(argsType),
			Tag:  reflect.StructTag(fmt.Sprintf(`cmd:"" name:%q help:%q`, word, word+" (out-of-process command plugin)")),
		},
	})
	return reflect.New(holderType).Interface()
}

// dispatchCommand routes a parsed dynamic command to its provider by PLACEMENT (F8): a
// COMPILED-IN command candy (registered in-proc as an inprocProvider — NOT a *grpcProvider and
// NOT a static builtin CommandProvider) dispatches IN-PROC via Invoke(OpRun), so the candy's
// handler runs inside charly's own process with native stdio/TTY; an OUT-OF-PROCESS command
// dispatches by syscall.Exec'ing its plugin binary (dispatchExternalCommand). This is the command
// half of placement-invisibility: the SAME command candy works compiled-in or out-of-process,
// the dynamic Kong grammar (externalCommandHolder) identical for both — only the dispatch
// transport differs. The dynamic grammar carries no Run() method, so dispatch is manual either way.
func dispatchCommand(d externalCommandDispatch) error {
	if prov, ok := providerRegistry.resolve(ClassCommand, d.word); ok {
		if _, external := prov.(*grpcProvider); !external {
			return dispatchInProcCommand(prov, d)
		}
	}
	return dispatchExternalCommand(d)
}

// dispatchInProcCommand forwards a compiled-in command's parsed pass-through args to its in-proc
// provider via Invoke(OpRun) — the candy's OpRun handler runs in charly's process (it owns
// os.Stdout/Stderr/TTY natively), mirroring the OUT-OF-PROCESS plugin's pass-through `{"args":[…]}`
// envelope (the OpRun contract), so a command candy behaves identically in either placement.
func dispatchInProcCommand(prov Provider, d externalCommandDispatch) error {
	params, err := marshalJSON(map[string]any{"args": externalCommandArgs(d)})
	if err != nil {
		return fmt.Errorf("command %q: marshal args: %w", d.word, err)
	}
	if _, err := prov.Invoke(context.Background(), &Operation{Reserved: d.word, Op: sdk.OpRun, Params: params}); err != nil {
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
func dispatchExternalCommand(d externalCommandDispatch) error {
	bin, argv, env, err := externalCommandExecPlan(d)
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
func externalCommandExecPlan(d externalCommandDispatch) (bin string, argv, env []string, err error) {
	args := externalCommandArgs(d)
	bin, err = resolveCommandPluginBinary(context.Background(), d.word)
	if err != nil {
		return "", nil, nil, err
	}
	argv = append([]string{bin}, args...)
	env = commandExecEnv()
	return bin, argv, env, nil
}

// externalCommandArgs reads the kong-populated pass-through Args out of the dynamic holder
// struct by reflection. Returns nil when the command was invoked with no positional args.
func externalCommandArgs(d externalCommandDispatch) []string {
	cmdField := reflect.ValueOf(d.holder).Elem().FieldByName(d.field)
	if cmdField.IsValid() && !cmdField.IsNil() {
		if a, ok := cmdField.Elem().FieldByName("Args").Interface().([]string); ok {
			return a
		}
	}
	return nil
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
func commandExecEnv() []string {
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
