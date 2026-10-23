## Implementation insights

Design notes for the Go-side architecture that aren't obvious from reading the source cold — consult before making structural changes.

### Kong flag-namespace collision

Top-level flags and subcommand flags share one global namespace: declaring the same flag on both `CLI` (`charly/main.go`) and a subcommand struct panics with `duplicate flag --<name>` at Kong parse time. Drop the subcommand flag and let users pass the top-level form; keep a subcommand flag only when it has no top-level twin. `candy/plugin-mcp/serve.go`'s `McpServeCmd` parses inside the plugin's own grammar, where `--no-default-repo` is a serve-local flag with no top-level twin.

### Env-var proxy for parent-flag detection

Code that cannot reach the parsed parent `CLI` struct reads the flag's bound env var instead: Kong populates env vars from flags, so `os.Getenv("CHARLY_PROJECT_DIR")` is a reliable proxy whether the user passed `--dir` or the env var. `projectDirPreParse` (`charly/plugin_command_prescan.go`) — the pre-`kong.Parse` command-word prescan — uses this to resolve the project dir. The externalized MCP server avoids the proxy entirely: `candy/plugin-mcp/serve.go` computes a managed `--dir`/`--repo` child-argv prefix per tool call (`computeProjectPrefix`), and `childCharlyEnv` strips `CHARLY_PROJECT_DIR`/`CHARLY_PROJECT_REPO` from the child env so the prefix stays authoritative.

### `yaml.v3` Node API is the reason edits preserve comments

Unmarshal-to-value + re-marshal scrambles comments, key order, and node styles. Every `charly.yml` editor in the authoring surface (`kit.SetByDotPath` in `sdk/kit/yaml.go`; `kit.AddBox` in `sdk/kit/scaffold.go`; `addCandyToBox` / `removeCandyFromBox` in `candy/plugin-authoring/authoring_edit.go` — moved from core in P14b; `appendCandyPackages` in `candy/plugin-candy/command.go`) navigates `*yaml.Node` trees directly and serializes with `yaml.Marshal(root)` only at the end. Tests (`sdk/kit/yaml_test.go`, `charly/scaffold_project_test.go`, `candy/plugin-authoring/authoring_edit_test.go`) verify that leading file comments, sibling keys, and per-key inline comments survive round trips.

### Scalar-to-sequence upgrade (scaffold `package:` null)

The layer scaffold writes `rpm:\n  packages:\n  # Add RPM packages here\n` — the value of `package:` parses as scalar-null, not a sequence, so a naive `candiesNode.Content = append(...)` silently no-ops. `appendCandyPackages` (`candy/plugin-candy/command.go`) checks `pkgsNode.Kind != yaml.SequenceNode` and upgrades in place (`Kind = yaml.SequenceNode; Tag = "!!seq"; Value = ""; Content = nil`), preserving the key+comment association on serialization. Any "upgrade a null scalar to a collection" path needs the same pattern.

### Path-traversal guard on the `box write` / `box cat` escape hatch

`resolveProjectFile(projectDir, relPath)` in `candy/plugin-authoring/authoring_edit.go` (P14b — moved from core `charly/scaffold_cmds.go`) is the single safety boundary for agent-driven file writes: it rejects absolute paths, calls `filepath.Clean`, then uses `filepath.Rel` + a prefix check to confirm the result stays inside the project root. Any future free-form file read/write verb goes through the same helper.

### Project-dir resolver is a multi-step resolver, not one

`charly/main.go` resolves the project dir in steps: `--repo` resolves to a cache path first (`spec.NormalizeRepoSpec` in `spec/spec/repo_identity.go`, then `charly/loader_threaded.go`'s `ResolveProjectRepo` → `loaderkit.EnsureRepoDownloaded` — the former `charly/main_repo.go` is DELETED, K-wave 2), then falls through into the `os.Chdir(cli.Dir)` block. The two paths are mutually exclusive (fast-fail if both are set). Downstream code just reads `os.Getwd()` — no per-command plumbing. Tested in `charly/main_repo_test.go` (hermetic via `CHARLY_REPO_CACHE` pre-seeding).
