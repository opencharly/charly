package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencharly/sdk"
)

// serve.go is the heart of the externalized `charly mcp serve` — it turns the entire
// charly CLI into an MCP server, ported out of the deleted charly/mcp_server.go. The one
// architectural change the externalization forces: the tool surface is built from the
// host's CLI MODEL (fork/exec `charly __cli-model`, decoded into sdk.CLIModel) instead of
// an in-process kong walk, and each tool call fork/execs `charly <cmd> <args>` instead of
// re-entering kong in-process (captureAndRun → forkCharly). The host stamps CHARLY_BIN
// (charly/plugin_transport.go) with the very binary that spawned this plugin, so the model
// fetch and every tool call drive the SAME charly.
//
// Project context: the original chdir'd once (bootstrapProject). Since this plugin
// fork/execs and cannot import charly's repo resolver (ResolveProjectRepo), the project
// context travels as a managed ARGS PREFIX prepended to EVERY charly fork/exec — see
// computeProjectPrefix.

// mcpSkipToolPaths are leaf paths never exposed as tools. The core `mcp serve` command no
// longer exists (it is THIS plugin's command), so its former path can't appear — but the
// skip set is kept (and covers the external `mcp` command word too) as a defensive guard
// against ever exposing a tool that re-invokes the server.
var mcpSkipToolPaths = map[string]bool{
	"mcp":       true,
	"mcp.serve": true,
}

// mcpSkipFlags are the root-global execution/project-context flags the MCP server manages
// CENTRALLY, so they are NOT re-exposed as per-tool properties: --repo / --dir set the
// project context (driven here by the managed project prefix; --repo and --dir are also
// mutually exclusive in charly's main, so a per-tool override would conflict with the
// prefix), and --host redirects execution to a remote SSH host (the server runs every tool
// against its single local charly). In the original in-process server these flags were
// effectively no-ops (main()'s startup resolution won and captureAndRun never re-ran
// main); excluding them from the fork/exec surface preserves that "server owns the
// context" semantic.
var mcpSkipFlags = map[string]bool{
	"repo": true,
	"dir":  true,
	"host": true,
}

// mcpDestructivePaths is the set of leaf command paths whose tools are annotated
// DestructiveHint=true and suppressed under --read-only. Ported VERBATIM from
// charly/mcp_server.go — it is MCP-tool policy, so it belongs with the MCP server. Keep
// this list deliberately conservative: a false negative exposes a dangerous tool to an
// unsuspecting LLM. Entries match the dotted leaf path (sdk.CLILeaf.Path).
var mcpDestructivePaths = map[string]bool{
	// Lifecycle
	"remove":          true,
	"stop":            true,
	"start":           true,
	"update":          true,
	"cmd":             true,
	"shell":           true,
	"service.restart": true,
	"service.start":   true,
	"service.stop":    true,
	// Image config / encrypted storage / quadlets
	"config.setup":   true,
	"config.mount":   true,
	"config.unmount": true,
	"config.passwd":  true,
	"config.remove":  true,
	// Secrets — top-level
	"secrets.set":    true,
	"secrets.delete": true,
	"secrets.import": true,
	"secrets.init":   true,
	// Secrets — GPG subtree (only the mutating leaves; read-only ones stay exposed)
	"secrets.gpg.setup":         true,
	"secrets.gpg.set":           true,
	"secrets.gpg.unset":         true,
	"secrets.gpg.edit":          true,
	"secrets.gpg.encrypt":       true,
	"secrets.gpg.add-recipient": true,
	"secrets.gpg.import-key":    true,
	// Deployment mutations
	"deploy.import": true,
	"deploy.reset":  true,
	// Image build/push/scaffold
	"box.build":       true,
	"box.merge":       true,
	"box.new.candy":   true,
	"box.new.project": true,
	"box.new.box":     true,
	// MCP-first authoring surface — mutates charly.yml or filesystem.
	// (box.fetch is idempotent + additive; box.cat is read-only — neither
	// is listed.)
	"box.set":       true,
	"box.add-candy": true,
	"box.rm-candy":  true,
	"box.refresh":   true, // deletes + re-clones cache
	"box.write":     true, // writes arbitrary file under project root
	// Candy authoring — mutates the candy manifest.
	"candy.set":     true,
	"candy.add-rpm": true,
	"candy.add-deb": true,
	"candy.add-pac": true,
	"candy.add-aur": true,
	// Benchmark — run mutates workspace + rebuilds images; self-evaluate
	// rebuilds images. Read-only siblings (scope, last-test-tag, list,
	// list-runners, report) stay exposed.
	"benchmark.run":           true,
	"benchmark.self-evaluate": true,
	// VM lifecycle
	"vm.create":  true,
	"vm.destroy": true,
	"vm.start":   true,
	"vm.stop":    true,
	"vm.build":   true,
	// udev / alias installation writes to the host
	"udev.install":    true,
	"udev.remove":     true,
	"alias.install":   true,
	"alias.uninstall": true,
	"alias.add":       true,
	"alias.remove":    true,
	// record / tmux state
	"record.start": true,
	"record.stop":  true,
	"record.cmd":   true,
	"tmux.kill":    true,
	"tmux.run":     true,
	"tmux.send":    true,
	"tmux.cmd":     true,
	// Settings mutations
	"settings.set":             true,
	"settings.reset":           true,
	"settings.migrate-secrets": true,
}

// projectFileName is the canonical project entry (charly's UnifiedFileName, which the
// plugin can't import from package main — a literal here).
const projectFileName = "charly.yml"

// runServe builds the MCP server from the host CLI model and runs the requested transport.
// Invoked from cliMain (CLI mode), where the plugin IS the process (charly syscall.Exec'd it),
// so its stdio/sockets are charly's inherited terminal streams. The ctx drives the stdio
// transport (it returns on stdin EOF / cancel); the HTTP server is bound to the process
// lifetime.
func runServe(ctx context.Context, c *McpServeCmd) error {
	bin, err := resolveCharlyBin()
	if err != nil {
		return fmt.Errorf("locate charly binary: %w", err)
	}
	server, count, err := buildMcpServer(bin, c.ReadOnly, c.NoDefaultRepo)
	if err != nil {
		return err
	}

	if c.Stdio {
		// The plugin runs in CLI mode (fork/exec'd by charly's command dispatch), so it owns
		// charly's inherited stdin/stdout/TTY natively — the go-sdk stdio transport reads
		// os.Stdin and writes os.Stdout directly, the editor/LLM integration path.
		return server.Run(ctx, &mcp.StdioTransport{})
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil)
	mux := http.NewServeMux()
	mux.Handle(c.Path, handler)
	fmt.Fprintf(os.Stderr, "charly mcp: serving %d tools on http://%s%s\n", count, c.Listen, c.Path)
	return http.ListenAndServe(c.Listen, mux)
}

// resolveCharlyBin picks the charly binary every fork/exec drives: CHARLY_BIN (the host
// stamps it with os.Executable() of the charly that spawned this plugin — see
// charly/plugin_transport.go) takes precedence; otherwise fall back to PATH.
func resolveCharlyBin() (string, error) {
	if b := os.Getenv("CHARLY_BIN"); b != "" {
		return b, nil
	}
	return exec.LookPath("charly")
}

// computeProjectPrefix replaces the original bootstrapProject's chdir. It returns the args
// prefix prepended to EVERY charly fork/exec so children resolve the project:
//   - charly.yml in cwd → no prefix (children inherit the plugin's cwd and find it);
//   - --no-default-repo (and no local charly.yml) → no prefix: still serve, but
//     project-dependent tools error at call time (the child reports "no project");
//   - otherwise → ["--repo", "default"]: the child resolves + fetches opencharly/charly.
//
// The plugin cannot import charly's ResolveProjectRepo, so the --repo default PREFIX
// delegates the resolution to the child charly — the fork/exec analogue of the original's
// chdir-into-the-cache.
func computeProjectPrefix(noDefaultRepo bool) []string {
	if _, err := os.Stat(projectFileName); err == nil {
		return nil
	}
	if noDefaultRepo {
		return nil
	}
	return []string{"--repo", "default"}
}

// buildMcpServer fetches the host CLI model and registers one tool per leaf. The
// (possibly downgraded) project prefix is closed over by each tool handler. Returns the
// server and the registered tool count for the startup banner.
func buildMcpServer(bin string, readOnly, noDefaultRepo bool) (*mcp.Server, int, error) {
	// __cli-model reflects the CORE CLI structure and needs NO project, so fetch it with no
	// prefix — it always succeeds. A --repo default here would fetch the default repo at
	// service startup and, if the pod's network is not ready yet, STICKILY downgrade the
	// project prefix for EVERY tool (the box.list.boxes "no charly.yml in /workspace" failure).
	model, err := fetchCLIModel(bin)
	if err != nil {
		return nil, 0, err
	}
	// The PROJECT prefix drives TOOL execution only — project tools (box.*) fall back to
	// --repo default when /workspace is empty; computed independently of the model fetch, so a
	// cold-start network blip can never strip it.
	prefix := computeProjectPrefix(noDefaultRepo)

	server := mcp.NewServer(&mcp.Implementation{Name: "charly", Version: model.Version}, nil)
	count := 0
	for i := range model.Leaves {
		leaf := model.Leaves[i]
		if leaf.Hidden || mcpSkipToolPaths[leaf.Path] {
			continue
		}
		destructive := mcpDestructivePaths[leaf.Path]
		if readOnly && destructive {
			continue
		}
		server.AddTool(cliLeafToTool(leaf, destructive), makeToolHandler(bin, prefix, leaf))
		count++
	}
	return server, count, nil
}

// fetchCLIModel runs `charly __cli-model` with NO prefix and decodes the model. The model is
// the CORE CLI's command tree — it does not depend on any project, so it always succeeds
// (no repo fetch). The PROJECT prefix is applied separately, per tool call, by the tool
// handlers (buildMcpServer's computeProjectPrefix), so a cold-start network blip can never
// strip project access from every tool.
func fetchCLIModel(bin string) (*sdk.CLIModel, error) {
	return runCLIModel(bin, nil)
}

// runCLIModel fork/execs the model emitter and decodes its stdout JSON.
func runCLIModel(bin string, prefix []string) (*sdk.CLIModel, error) {
	argv := append(append([]string{}, prefix...), "__cli-model")
	cmd := exec.Command(bin, argv...)
	cmd.Env = childCharlyEnv() // clear CHARLY_PROJECT_DIR so --repo default doesn't conflict
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w (stderr: %s)", bin, strings.Join(argv, " "), err, strings.TrimSpace(errBuf.String()))
	}
	var model sdk.CLIModel
	if err := json.Unmarshal(out.Bytes(), &model); err != nil {
		return nil, fmt.Errorf("decode cli-model JSON: %w", err)
	}
	return &model, nil
}

// ---------------------------------------------------------------------------
// CLI model → MCP tool schema (ports kongLeafToTool + valueToSchema over sdk.CLIArg).
// ---------------------------------------------------------------------------

// cliLeafToTool converts one CLI leaf into an *mcp.Tool with a JSON-Schema object built
// from its positionals and flags. additionalProperties:false keeps the LLM honest;
// ReadOnlyHint/DestructiveHint annotations mirror the original exactly.
func cliLeafToTool(leaf sdk.CLILeaf, destructive bool) *mcp.Tool {
	props := map[string]any{}
	var required []string

	for _, pos := range leaf.Positionals {
		props[pos.Prop] = argToSchema(pos)
		if pos.Required {
			required = append(required, pos.Prop)
		}
	}
	for _, f := range leaf.Flags {
		if mcpSkipFlags[f.Name] {
			continue
		}
		if _, exists := props[f.Prop]; exists {
			continue
		}
		props[f.Prop] = argToSchema(f)
		if f.Required {
			required = append(required, f.Prop)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	schema["additionalProperties"] = false

	desc := strings.TrimSpace(leaf.Help)
	if desc == "" {
		desc = "charly " + strings.ReplaceAll(leaf.Path, ".", " ")
	}
	if destructive {
		desc += " [destructive: mutates container, volume, or host state]"
	}

	tool := &mcp.Tool{Name: leaf.Path, Description: desc, InputSchema: schema}
	if destructive {
		yes := true
		tool.Annotations = &mcp.ToolAnnotations{DestructiveHint: &yes}
	} else {
		tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
	}
	return tool
}

// argToSchema produces a JSON-Schema fragment for one CLI arg (positional or flag). The
// CLI model already carries the inferred facts (Type/Enum/Default+HasDefault/IsSlice+
// ElemType/IsMap), so this needs no reflect/kong.
func argToSchema(a sdk.CLIArg) map[string]any {
	out := map[string]any{}
	if a.Help != "" {
		out["description"] = a.Help
	}
	if len(a.Enum) > 0 {
		anyEnum := make([]any, len(a.Enum))
		for i, e := range a.Enum {
			anyEnum[i] = e
		}
		out["enum"] = anyEnum
	}

	if a.IsSlice {
		out["type"] = "array"
		elem := a.ElemType
		if elem == "" {
			elem = "string"
		}
		out["items"] = map[string]any{"type": elem}
		return out
	}
	if a.IsMap {
		out["type"] = "object"
		out["additionalProperties"] = map[string]any{"type": "string"}
		return out
	}

	t := a.Type
	if t == "" {
		t = "string"
	}
	out["type"] = t

	if a.HasDefault && a.Default != "" {
		// Coerce the default string into the schema's type so JSON-Schema validators accept
		// it (mirrors the original valueToSchema).
		switch t {
		case "boolean":
			if b, err := strconv.ParseBool(a.Default); err == nil {
				out["default"] = b
			}
		case "integer":
			if i, err := strconv.ParseInt(a.Default, 10, 64); err == nil {
				out["default"] = i
			}
		case "number":
			if f, err := strconv.ParseFloat(a.Default, 64); err == nil {
				out["default"] = f
			}
		default:
			out["default"] = a.Default
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Tool handler: JSON input → argv → fork/exec charly (ports argvFromJSON / flagToArgv /
// scalarToString; captureAndRun → forkCharly).
// ---------------------------------------------------------------------------

// makeToolHandler closes over the resolved binary, the project prefix, and the leaf model,
// returning an MCP ToolHandler that reconstructs an argv and fork/execs charly. No global
// stream mutex is needed (unlike the original's runMu): fork/exec captures each call's
// stdout/stderr into private buffers, so concurrent tool calls never interleave.
func makeToolHandler(bin string, prefix []string, leaf sdk.CLILeaf) mcp.ToolHandler {
	posByProp := map[string]sdk.CLIArg{}
	posOrder := make([]string, 0, len(leaf.Positionals))
	for _, pos := range leaf.Positionals {
		posByProp[pos.Prop] = pos
		posOrder = append(posOrder, pos.Prop)
	}
	flagByProp := map[string]sdk.CLIArg{}
	for _, f := range leaf.Flags {
		if mcpSkipFlags[f.Name] {
			continue
		}
		flagByProp[f.Prop] = f
	}
	cmdTokens := strings.Split(leaf.Path, ".")

	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input map[string]any
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
				return toolError(fmt.Errorf("parsing arguments: %w", err)), nil
			}
		}
		if input == nil {
			input = map[string]any{}
		}

		cmdArgs, err := argvFromJSON(posOrder, posByProp, flagByProp, input)
		if err != nil {
			return toolError(err), nil
		}
		// charly <projectPrefix…> <cmdTokens…> <args…>  — the prefix carries the project
		// context, the global flags charly parses before the subcommand.
		argv := make([]string, 0, len(prefix)+len(cmdTokens)+len(cmdArgs))
		argv = append(argv, prefix...)
		argv = append(argv, cmdTokens...)
		argv = append(argv, cmdArgs...)

		stdout, stderr, runErr := forkCharly(ctx, bin, argv)

		res := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: assembleToolText(stdout, stderr, runErr)}},
		}
		if runErr != nil {
			res.IsError = true
		}
		return res, nil
	}
}

// argvFromJSON reconstructs the per-command args (flags then positionals) from MCP JSON.
// Flags come first (sorted for determinism; booleans emit --flag / --no-flag with no
// value); positionals follow in declared order; cumulative slices expand. The leading
// command tokens + project prefix are prepended by the caller.
func argvFromJSON(posOrder []string, posByProp, flagByProp map[string]sdk.CLIArg, input map[string]any) ([]string, error) {
	var args []string

	for _, name := range sortedKeys(input) {
		if _, isPos := posByProp[name]; isPos {
			continue
		}
		f, ok := flagByProp[name]
		if !ok {
			// additionalProperties:false makes the SDK reject unknown keys before the
			// handler; reaching here means a managed global flag (repo/dir/host) the server
			// intentionally does not forward, or an out-of-schema key.
			return nil, fmt.Errorf("unknown or unsupported argument %q for tool", name)
		}
		tokens, err := flagToArgv(f, input[name])
		if err != nil {
			return nil, fmt.Errorf("flag --%s: %w", f.Name, err)
		}
		args = append(args, tokens...)
	}

	for _, name := range posOrder {
		v, ok := input[name]
		if !ok {
			continue // kong errors if a required positional is missing
		}
		// Cumulative positionals (e.g. box build <boxes…>) accept multiple values.
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				s, err := scalarToString(item)
				if err != nil {
					return nil, fmt.Errorf("positional %s: %w", name, err)
				}
				args = append(args, s)
			}
			continue
		}
		s, err := scalarToString(v)
		if err != nil {
			return nil, fmt.Errorf("positional %s: %w", name, err)
		}
		args = append(args, s)
	}
	return args, nil
}

// flagToArgv renders a single flag + value as CLI tokens, using the model's CLIArg facts
// (.Name for --Name=…, .IsBool / .Negated for boolean shape).
func flagToArgv(f sdk.CLIArg, v any) ([]string, error) {
	if f.IsBool {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("expected boolean, got %T", v)
		}
		if b {
			return []string{"--" + f.Name}, nil
		}
		if f.Negated {
			return []string{"--no-" + f.Name}, nil
		}
		return nil, nil
	}

	if arr, ok := v.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			s, err := scalarToString(item)
			if err != nil {
				return nil, err
			}
			out = append(out, "--"+f.Name+"="+s)
		}
		return out, nil
	}

	s, err := scalarToString(v)
	if err != nil {
		return nil, err
	}
	return []string{"--" + f.Name + "=" + s}, nil
}

// scalarToString coerces a JSON scalar into the string Kong expects on the command line.
func scalarToString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10), nil
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case json.Number:
		return string(x), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("unsupported scalar type %T", v)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// forkCharly runs `charly <argv…>` and captures stdout/stderr/exit. It REPLACES the
// original captureAndRun (in-process kctx.Run with os.Stdout redirection) — fork/exec
// isolates every call's streams, so the whole os.Stdout/os.Stderr-capture-by-pointer
// machinery (and its runMu serialization) is gone.
// childCharlyEnv is os.Environ() with CHARLY_PROJECT_DIR / CHARLY_PROJECT_REPO stripped. The
// project context for every charly child is driven EXPLICITLY by the computeProjectPrefix argv
// (--repo default, or the inherited cwd when /workspace carries a charly.yml); leaving
// CHARLY_PROJECT_DIR set (the deployed container sets it to /workspace) makes charly read it as
// --dir, which COLLIDES with the --repo prefix ("--repo and --dir are mutually exclusive").
// Shared by forkCharly (tool calls) AND runCLIModel (the startup __cli-model fetch) — BOTH must
// clear it, or the model fetch fails, fetchCLIModel downgrades to the no-prefix path, and every
// project-dependent tool (box.*) then runs without --repo default and errors "no charly.yml".
func childCharlyEnv() []string {
	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, "CHARLY_PROJECT_DIR=") || strings.HasPrefix(e, "CHARLY_PROJECT_REPO=") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

func forkCharly(ctx context.Context, bin string, argv []string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Env = childCharlyEnv()
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// assembleToolText composes the TextContent payload: stdout first, stderr as a labelled
// block, then an error summary (ported verbatim from charly/mcp_server.go).
func assembleToolText(stdout, stderr string, runErr error) string {
	var b strings.Builder
	b.WriteString(stdout)
	if stderr != "" {
		if b.Len() > 0 && !strings.HasSuffix(stdout, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("--- stderr ---\n")
		b.WriteString(stderr)
	}
	if runErr != nil {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "--- error ---\n%v\n", runErr)
	}
	return b.String()
}

func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}
