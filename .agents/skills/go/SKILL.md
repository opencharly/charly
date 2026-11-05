---
name: go
description: |-
  Go CLI development: building the charly binary, running tests, source structure, and the SDD operationalization (spec/schema/*.cue -> `charly task cue-gen`). Use before reading/modifying charly Go source or spec/schema/*.cue.
---

# Go - CLI Development

## Overview

The `charly` CLI is a Go program in the `charly/` directory. It uses the Kong CLI framework, go-containerregistry for OCI operations, and YAML parsing for configuration. All computation, validation, and building logic lives in Go. The non-charly entrypoint is the bootstrap build `scripts/bootstrap-charly.sh`; the repository's maintenance surface is the `kind: task` entities in `charly.yml`, run via `charly task`. (A few umbrella-only helper scripts that generate PR bodies / splice the dispatcher are invoked BY those tasks.)

## Reference Index

| Topic | File |
|---|---|
| Architecture deep dives (unified YAML loader, Schema Driven Design pipeline + generation-coverage catalog, the `spec/spec` package, namespace/remote-layer resolvers, Capabilities, the kubernetes/VM external substrates, YAML↔Go conventions, Kong parent+leaf commands, mode purity, the InstallPlan IR, the VM-path module topology, self-exec coordination) and the full file-by-file Source Code Map | `references/source-map.md` |
| The step-by-step recipe for changing the `charly.yml` schema (CUE is the single source of truth) | `references/schema-change-recipe.md` |
| Design notes for the Go-side architecture not obvious from reading the source cold (Kong flag-namespace collision, env-var proxy for parent-flag detection, the `yaml.v3` Node API, scalar-to-sequence upgrades, the path-traversal guard, the two-step project-dir resolver) | `references/implementation-insights.md` |

## Quick Reference

| Action | Command | Description |
|--------|---------|-------------|
| Build | `scripts/bootstrap-charly.sh` | Compile to `bin/charly` (CalVer-stamped), NO install. The charly-dev candy (declared in the repo-root `charly.yml`) copies THIS file directly — there is no second path to keep in sync. Does NOT touch any packaging source (the native packages are built by the `charly generate-packages` plugin from `packaging/charly.yml`'s `packaging:` section) |
| Package | `charly generate-packages` (nFPM plugin) | Build a distro-native `.pkg.tar.zst`/`.rpm`/`.deb`/`.apk`/`.ipk` release artifact via the `charly generate-packages` plugin (nFPM, `sdk/packagekit`), published to the per-distro package repos. Install it yourself with your OWN package manager (`pacman -U`/`dnf install`/`apt install`/`apk add`) |
| Install (portable) | `scripts/bootstrap-charly.sh --install` | Copy `bin/charly` to `$HOME/.local/bin/charly` (solo bootstrap; NOT a multi-teammate dev-loop step — see below) |
| Run tests | `cd charly && go test ./...` | Run all tests |
| Run specific test | `cd charly && go test -run TestName ./...` | Run single test |
| Vet | `cd charly && go vet ./...` | Static analysis |
| Format | `cd charly && gofmt -w .` | Format code |

In a multi-teammate / multi-worktree setup, NO task installs to the
host during in-flight work — use `scripts/bootstrap-charly.sh` per worktree instead; see
`/charly-internals:agents` "The charly binary in a multi-teammate /
multi-worktree setup" for the full discipline.

## Project Directory Structure

```
project/
├── bin/charly                     # Built by `scripts/bootstrap-charly.sh` (gitignored)
├── charly/                        # Go module (kong CLI, go-containerregistry)
│   └── charly.yml             # The binary's embedded default config (//go:embed,
│                              # embed_defaults.go): distro/builder/init/resource
│                              # build vocabulary + the sidecar: template library.
│                              # Parsed by the SAME unified loader as any project
│                              # charly.yml; a project ships none of it.
├── .build/                    # Generated Containerfiles (gitignored)
├── charly.yml                  # Image definitions + the kind:task maintenance surface
├── scripts/bootstrap-charly.sh # The ONE non-charly entrypoint (builds bin/charly)
├── candy/<name>/             # Layer directories
├── marketplace/                   # Git submodule (opencharly/plugins)
├── docs/                      # Git submodule (opencharly/docs)
└── templates/                 # supervisord.header.conf (referenced by init.supervisord.header_file)
```

Submodule convention: `marketplace/` (and `docs/`, `box/<distro>`) are submodules rooted at
their own repos; the sdk contract module (`github.com/opencharly/sdk`) is NOT a
submodule — it resolves from the module proxy at the pinned require version in every
go.mod. Clone with `--recurse-submodules` (no sdk checkout exists to fetch) or run
`git submodule update --init` after a plain clone. See
`/charly-internals:skills` for the skill-authoring and sync conventions.

## Go Module Info

- Go version: 1.26.0
- Key dependencies: `kong` (CLI), `go-containerregistry` (OCI), and `github.com/opencharly/sdk` (the plugin contract module — required at the shared pinned version, resolved from the module proxy; there is no `replace`). The credential store's `go-keyring` (Secret Service API) is NOT a core dependency — it links only into the out-of-process `candy/plugin-secrets` plugin (the C2 dep-shed)
- Module path: `charly/go.mod`

## Common Workflows

### Add a New CLI Command

1. Define command struct in appropriate file (or new file)
2. Add to CLI struct in `main.go`
3. Implement `Run()` method
4. Add tests in `*_test.go`
5. Build and test: `cd charly && go test ./... && go build -o ../bin/charly .`

### Add a New Validation Rule

A host-natural check that needs the raw loader goes in `candy/plugin-box/validate_schema_rules.go` (the former `charly/validate.go` is DELETED, K-wave 2); the
per-kind/op/candy/graph rule engine lives in the compiled-in `command:box` plugin
(`candy/plugin-box`) over the resolved-project envelope — add per-entity/op rules
there.

See `references/schema-change-recipe.md` for the full recipe when the change touches the `charly.yml` schema itself (CUE is the single source of truth).

### Debug a Build Issue

```bash
# Generate Containerfiles without building
bin/charly box generate

# Inspect generated output
cat .build/<image>/Containerfile

# Validate configuration
bin/charly box validate

# Inspect resolved image config
bin/charly box inspect <image>
```

### Intermediate image cache invalidation

`charly box build` auto-generates intermediate images (e.g., `ghcr.io/opencharly/charly-fedora-2-dbus-nodejs`) that aggregate the `charly` layer plus common layers for cache reuse across many downstream images. These intermediates are aggressively podman-cached. Updating `bin/charly` does invalidate the COPY step inside the intermediate, but if the intermediate tag already exists locally, `charly box build` may reuse it without re-running the build chain. To force a fresh binary propagation after a `bin/charly` rebuild:

```bash
charly clean --invalidate 'charly-fedora-2*'
charly box build <image>
```

There is no longer a dual-path gotcha here. `bin/charly` at the repo root is the ONE path: host-side invocations use it, and the charly-dev candy's `copy: bin/charly` resolves against the repo root and reads the same file. It used to be two paths kept in step by `scripts/bootstrap-charly.sh`, and forgetting the sync baked the previous binary into every check bed — silently, because nothing failed. Declaring the candy where the path already resolves removed the second path and that whole class of mistake.

## Environment constraints — sandboxed /tmp + Go temp dirs

Some dev hosts (a sandboxed runner, a small tmpfs `/tmp`) cap writes to the system
temp dir at a few tens of MB per command, regardless of the free space `df` reports.
Go builds and golangci-lint routinely exceed that, and the failures look like tool
bugs ("disk quota exceeded", "parallel golangci-lint is running") — they are
environmental, not code defects. On such a host:

- Run Go builds/tests with `GOTMPDIR=~/.cache/go-tmp` (a root-fs dir) — Go's build
  work then never touches the capped temp dir.
- golangci-lint v2 locks at `$TMPDIR/golangci-lint.lock`; a missing TMPDIR dir makes
  the lock open fail with ENOENT, which golangci-lint reports as "parallel golangci-lint
  is running" — a false positive, not a real parallel run. Create the TMPDIR dir first
  (or pass `--allow-parallel-runners` with a fresh cache). See `/charly-internals:go-quality`.
- **Keep `$TMPDIR` short for any suite that connects a plugin.** go-plugin serves
  each plugin over a UNIX socket at `$TMPDIR/plugin<random digits>`, and `sun_path`
  caps the whole path at 107 BYTES — leaving `$TMPDIR` a little under 90 bytes once
  the `/plugin<digits>` suffix is accounted for. The digit count varies, so a path
  near the budget fails intermittently: the same directory passes, then fails,
  unchanged. Use a short path such as `/tmp/cs` and leave headroom.

  **This does not contradict the `GOTMPDIR=~/.cache/go-tmp` rule above; the two
  variables go to different places for different reasons.** `GOTMPDIR` carries Go's
  build work, which is large, so it moves OFF the quota-capped temp dir and may be
  long — no socket is created there. `$TMPDIR` carries the plugin socket, which is a
  few bytes, so the quota is irrelevant TO THE SOCKET and the binding constraint on
  `$TMPDIR` is path LENGTH. Short-and-on-`/tmp` for `$TMPDIR`, long-and-off-`/tmp`
  for `GOTMPDIR`. (`/tmp` still has a cumulative cap — see the last bullet in this
  section — and golangci-lint's lock also lives under `$TMPDIR`; neither changes
  where the socket must go.)

  Over budget, `bind` returns `invalid argument` and the loader reports
  `Unrecognized remote plugin message` / `Failed to read any lines from plugin's
  stdout`, blaming architecture, libraries, or permissions — none of which is the
  cause. The visible failure is a downstream assertion, e.g.
  `TestExternalStructKind_StructuralDecode` reporting `structural plugin kind not
  folded into uf.Deploy; have deploy keys []`.

- **`CHARLY_PLUGIN_DIR` prepends; `CHARLY_PLUGIN_ONLY=1` is what excludes.** A word
  is **BAKED** when a provider binary for it sits in `/usr/lib/charly/plugins/`
  beside a `<name>.providers` manifest naming that word — put there by the OS
  package, or by a candy's `bake_plugin:` step at image-build time. Baked-ness is
  per WORD, not per machine: `cat /usr/lib/charly/plugins/*.providers` lists exactly
  which words are baked on the host you are standing on.
  `bakedPluginDirs()` returns `$CHARLY_PLUGIN_DIR` when set and then appends the FHS
  `/usr/lib/charly/plugins`, so pointing the dir at an empty directory masks nothing.
  `CHARLY_PLUGIN_ONLY=1` drops the FHS path, making the search exactly
  `$CHARLY_PLUGIN_DIR` (or empty when that is unset). It is an EXACT `1` match, so a
  typo leaves the FHS path in place. That is safe for a deployed container — it keeps
  finding its own baked plugins — but it is NOT safe for the test below, where the
  same typo produces a silently ineffective flag. Which is why that procedure leads
  with a control rather than trusting the variable was read.

  **Baked lookup is keyed by WORD and short-circuits the project scan.**
  `resolveCommandPluginBinary` returns on a baked hit before the project loads: a
  baked word resolves to the baked binary and its project declaration is never
  consulted, while an unbaked word falls through to the project scan normally. So
  `charly examplecommand hello world` prints `hello world` from the project candy
  even where the charly package is installed — `examplecommand` is not baked.

  To exercise a BAKED word's project declaration, drop the FHS path. Worked with
  `generate-packages`. **Check the two preconditions first** — the procedure is only
  meaningful for a word that is both baked and declared:

  ```
  $ cat /usr/lib/charly/plugins/*.providers | grep -c '^command:generate-packages$'
  1
  $ ls candy/generate-packages/charly.yml | wc -l
  1
  ```

  Then run the two arms. **The `Note: local candy … shadows …` preamble is the
  signal, not noise** — it appears because the flag made charly load the PROJECT.
  Check for it with a predicate rather than by counting; the preamble is two identical
  lines per shadowed candy (the project loads twice); the set varies by host and the
  ordering varies from run to run:

  ```
  $ CHARLY_PLUGIN_ONLY=1 ./bin/charly generate-packages 2>&1 \
      | grep -q '^Note: local candy' && echo "project loaded" || echo "baked short-circuit"
  project loaded

  $ ./bin/charly generate-packages 2>&1 \
      | grep -q '^Note: local candy' && echo "project loaded" || echo "baked short-circuit"
  baked short-circuit
  ```

  **Run that control first.** Without it you cannot tell a working flag from a
  silently ineffective one: `CHARLY_PLUGIN_ONLY` is an EXACT `1` match, so a typo
  leaves the FHS path in place — and the last line of the run is BYTE-IDENTICAL with
  and without the flag. A `tail -1` alone therefore has exactly the property this
  section opens by warning about: it cannot distinguish the two sources.

  With the control passing, the arms read the resolution itself:

  ```
  $ CHARLY_PLUGIN_ONLY=1 ./bin/charly generate-packages 2>&1 | tail -1
  generate-packages: missing flags: --arch=STRING, --binary=STRING, --candy=STRING, --out=STRING, --plugins=STRING, --version=STRING

  $ mv candy/generate-packages /tmp/gp-aside
  $ CHARLY_PLUGIN_ONLY=1 ./bin/charly generate-packages 2>&1 | tail -1
  charly: error: unexpected argument generate-packages
  $ mv /tmp/gp-aside candy/generate-packages
  ```

  **What each step licenses.** The control establishes that the flag took effect and
  the project was loaded. Given that, arm 1 succeeding shows a PROJECT candy served
  the word — with `CHARLY_PLUGIN_DIR` unset the baked search is empty, so nothing else
  could have. Arm 2 attributes it to `candy/generate-packages` specifically, by
  removing that one candy and watching the word stop resolving.

  Both arms need the flag. Without it, arm 2 still succeeds — the baked binary
  answers once the project candy is gone — so the removal would tell you nothing.

- `.claude/hooks/pre-commit-gate.sh` (the staged-Go-lint discipline backstop) redirects
  its own lint temp dirs to `~/.cache/charly-gate-lint/` and creates the TMPDIR/GOTMPDIR
  subdirs, so it is unaffected by the cap wherever it runs. It is wired as a
  `PreToolUse(Bash)` hook in every harness that roots here — `.claude/settings.json`
  (Claude Code, since umbrella `5e485a8`), `.reasonix/settings.json`, and
  `~/.kimi-code/config.toml` which delegates to `.claude/hooks/` — so under Claude Code
  the staged-Go-lint smoke runs automatically at commit time (the two bullets above
  still apply for anything the hook does not cover).
- **Cumulative /tmp usage cap — the Bash tool's output capture dies.** Beyond the
  per-command cap, the sandbox also caps TOTAL /tmp usage (observed at ~80% of the
  tmpfs). When /tmp fills to that point, the Bash tool's output capture fails: every
  command that writes to stdout/stderr exits 1 with no output, `python3` exits 120
  (Python's "failed to flush stdout at shutdown" code), while no-output commands
  (`true`, `echo x > /dev/null`) still succeed. The underlying error is
  `write error: Disk quota exceeded`. Fix: clear the accumulated `tmp.*` Go temp
  dirs (and other junk) in /tmp to drop usage below the cap — on this host 17G of
  `tmp.*` dirs had accumulated from lint runs and other operations. A session
  restart also resets it (fresh sandbox quota).

## R9 — deployed binary matches source; runtime deps live in the PKGBUILD

See the project rulebook's R9 mandate (`AGENTS.md`). Applied to the `charly` toolchain:

- **Syncing source does not rebuild the binary.** Syncthing / git / rsync move
  *source* between hosts. After pushing code, rebuild on the target — `scripts/bootstrap-charly.sh` in that checkout — and verify `./bin/charly version` matches what
  you built — if the version is old, the fix under test isn't really under test.
  The freshness guard (`/charly-internals:agents` "The charly binary in a
  multi-teammate / multi-worktree setup") catches a stale invoked binary against
  newer `charly/*.go` in the same tree, but the version check is still the
  explicit proof — the per-worktree-vs-host-package split lives there too.
- **Every runtime OS dependency goes into the charly candy's `packaging:`
  section** (`packaging/charly.yml` `packaging.formats.*.depends`) — the
  single source of truth (`nc`, `socat`, `xorriso`, `qemu-guest-agent`, …),
  read by the `charly generate-packages` plugin (sdk/packagekit). A manual
  install on one host is a bug report disguised as a fix — it won't survive a
  fresh install on a synced host.

The verification side (checking the deployed binary + deps on a live target)
is `/charly-check:check` Standards 7–9; the dual-path `bin/charly` ↔
single-path `bin/charly` note is above and in `/charly-tools:charly`.

## Style Guide

- All logic belongs in Go. The non-charly entrypoint is the bootstrap build (`scripts/bootstrap-charly.sh`).
- Bootstrap build only (shell), Go for all other logic.
- Test files alongside source files (`foo.go` -> `foo_test.go`).

## Cross-References

- `/charly-internals:generate-source` — Understanding generated Containerfiles + deep dive on the task emission pipeline (`sdk/deploykit/tasks_emit.go`).
- `/charly-image:layer` — **Canonical author-facing reference** for the task verb catalog that `sdk/deploykit/tasks_emit.go` implements.
- `/charly-build:validate` — Validation rules and error handling (`validateCandyTasks` in `candy/plugin-box/validate_rules.go`).
- `/charly-build:build` — Using the built CLI.
- `/charly-check:check` — Author-facing reference for the declarative-testing feature that `checkspec.go` / `planrun_adapter.go` (the op-context grammar) / `checkrun.go` / `checkrun_charly_verbs.go` / `description_collect.go` / `check_cmd.go` / `check_endpoint_resolve.go` / `check_graphics_endpoint.go` (the host-endpoint reverse-legs) implement — plus `sdk/kit/checkvars.go` and `sdk/kit/local_image.go` (moved out of core in P12a). (Op-level check validation moved out of core to `candy/plugin-box/validate_check.go`; the `charly check` CLI + every check-run mode body live in the compiled-in `command:check` plugin `candy/plugin-check/`.)
- `/charly-build:charly-mcp-cmd` — Author-facing reference for both (a) the declarative `mcp:` client check verb (method catalog, URL-rewrite behavior, port-publishing gotcha, transport dispatch — served out-of-process by `candy/plugin-mcp`, which resolves its endpoint via the `check_endpoint_resolve.go` reverse-legs) and (b) the `charly mcp serve` server (externalized to `candy/plugin-mcp` `command:mcp`: one tool per CLI leaf, auto-generated from the `charly __cli-model` reflection seam, destructive-hint + `--read-only` filter, Streamable-HTTP + stdio transports, auto-fallback to `opencharly/charly` — pair with `cli_model_cmd.go` + `main_repo.go` + `box_fetch_reentry.go` + `candy/plugin-authoring` + `sdk/kit/yaml.go` in `references/source-map.md`).
- `/charly-coder:charly-mcp` — The candy that deploys `charly mcp serve` inside a container: bind-mount volume NAME `project` at the container PATH `/workspace`, `CHARLY_PROJECT_DIR=/workspace` so build-mode MCP tools (`box.list.boxes`, `box.inspect`, etc.) reach `charly.yml` from outside the project checkout — or auto-fall back to `opencharly/charly` when `/workspace` is empty (the fallback fires on absence of charly.yml, not absence of CHARLY_PROJECT_DIR).
- `/charly-check:wl`, `/charly-check:cdp`, `/charly-check:vnc`, and `/charly-check:dbus` are out-of-process verbs served by `candy/plugin-wl` / `candy/plugin-cdp` / `candy/plugin-vnc` / `candy/plugin-dbus` (cdp/vnc resolve their endpoints via the `check_endpoint_resolve.go` reverse-legs; `wl`/`dbus` are EXEC-based and reach the venue over the executor).
- Source: `charly/` directory (~304 source + ~294 test .go files).

## When to Use This Skill

**MUST be invoked** before reading or modifying Go source files. Invoke this skill BEFORE launching Explore agents on charly/ code.

Live-deploy verification: see `/charly-check:check` (the 11 Testing Standards) and `/charly-internals:disposable`.
