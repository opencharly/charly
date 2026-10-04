#!/usr/bin/env bash
# bootstrap-charly.sh — build this worktree's ./bin/charly.
#
# The ONE non-charly entrypoint, by necessity: a `charly task` can only run once a
# charly binary exists, so the build that PRODUCES that binary cannot itself be a
# charly task. Everything else a repository needs (push, setup, cue-gen, mods-tidy,
# verify, …) is a `kind: task` entity in charly.yml, run via `./bin/charly task <name>`.
#
# This replaces Taskfile's `build:binary` / `setup:all` / `build:install-portable`:
#   ./scripts/bootstrap-charly.sh              # build to bin/charly (the default)
#   ./scripts/bootstrap-charly.sh --install    # also install to $HOME/.local/bin
#   ./scripts/bootstrap-charly.sh --dev-plugin <candy-name>=<repo-checkout> …
#                                              # DEV BUILD (see below), repeatable
#
# --dev-plugin builds a compiled-in plugin FROM A LOCAL CHECKOUT instead of its pinned
# module-proxy tag, so a bed run on this binary exercises the plugin's UNMERGED source
# (opencharly/charly#775). <repo-checkout> is that plugin's repo root; its candy/<name>
# must declare module github.com/opencharly/<name>/candy/<name>. pluginsgen emits the
# go.work.dev workspace that resolves it and this build selects it with GOWORK, so the
# committed go.work / go.work.sum are untouched and no plain build can inherit the
# override. NOT a release build — never install one.
#
# Usage: run from the repo root (any worktree of a charly checkout).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Arguments, parsed up front so an unknown flag is an error instead of being ignored.
# dev_specs holds the raw <candy-name>=<repo-checkout> specs; an array, never a string,
# because a checkout path may contain spaces.
install=0
dev_specs=()
while [ $# -gt 0 ]; do
  case "$1" in
    --install) install=1; shift ;;
    --dev-plugin)
      [ $# -ge 2 ] || { echo "bootstrap-charly: --dev-plugin needs <candy-name>=<repo-checkout>" >&2; exit 2; }
      dev_specs+=("$2"); shift 2 ;;
    --dev-plugin=*) dev_specs+=("${1#--dev-plugin=}"); shift ;;
    *) echo "bootstrap-charly: unknown argument: $1" >&2; exit 2 ;;
  esac
done

mkdir -p bin

# Regenerate the compiled-in plugin wiring from charly.yml `compiled_plugins:` BEFORE
# the go build that needs it: pluginsgen emits charly/plugins_generated.go
# (registerCompiledPlugin per selected plugin candy) + the repo-root go.work. GOWORK=off
# so a stale go.work can't fail workspace load before regeneration; the generator
# imports only stdlib+yaml and runs without a pre-built charly.
#
# With --dev-plugin the generator also writes go.work.dev (the dev workspace, selected
# below with GOWORK) and — on a run WITHOUT one — deletes it, so a stale dev workspace can
# never resolve a local checkout for a later plain build.
# Both below spell an array expansion as ${a[@]+"${a[@]}"}: it expands to nothing for an
# EMPTY array under `set -u` on every bash, where a bare "${a[@]}" is an unbound-variable
# error on a bash older than 4.4 — and the no-override build is the common case.
dev_flags=()
for spec in ${dev_specs[@]+"${dev_specs[@]}"}; do dev_flags+=(-dev-plugin "$spec"); done

echo "bootstrap-charly: regenerating compiled-in plugin wiring"
(cd charly && GOWORK=off go run ./internal/pluginsgen \
  -root .. -config charly/charly.yml \
  -out charly/plugins_generated.go -gowork go.work -gowork-dev go.work.dev \
  -outrefs charly/plugins_refs_generated.go -corpus charly/plugin_corpus.txt \
  ${dev_flags[@]+"${dev_flags[@]}"})

# Stamp the binary's CalVer identity (`charly version` -> main.BuildCalVer) at build
# time, from the shared scripts/calver.sh — ALWAYS the HEAD commit's UTC date
# (deterministic: same commit -> same version, clean or dirty). Without this stamp
# `charly version` would read the wall clock at invocation.
# Build to a temp path FIRST so a guard failure below can never leave a half-written
# bin/charly in place; promotion happens only after every check passes.
# -buildvcs=false: every charly binary build passes it (the VCS stamp has zero
# consumers, and workspace-mode Go's VCS-status walk breaks in a linked worktree
# outside the main repo path).
# A --dev-plugin build resolves through go.work.dev (the generator wrote it just above);
# every other build resolves through the committed go.work. The choice is made HERE, in
# one place, and never by editing the tracked workspace — which is why the override
# survives the regeneration that would wipe a hand-added `use` line.
if [ ${#dev_specs[@]} -gt 0 ]; then
  GOWORK_PATH="$ROOT/go.work.dev"
  echo "bootstrap-charly: DEV BUILD — NOT A RELEASE BUILD." >&2
  echo "bootstrap-charly: resolving compiled-in plugins from LOCAL checkouts:" >&2
  for spec in "${dev_specs[@]}"; do echo "bootstrap-charly:   $spec" >&2; done
  echo "bootstrap-charly: the committed go.work / go.work.sum are not used or touched." >&2
else
  GOWORK_PATH="$ROOT/go.work"
fi

echo "bootstrap-charly: building bin/charly"
CALVER="$(bash scripts/calver.sh)"
(cd charly && GOWORK="$GOWORK_PATH" go build -buildvcs=false \
  -ldflags "-X main.BuildCalVer=${CALVER}" -o ../bin/.charly.next .)

# Workspace-mode Go may extend a workspace's checksum lock when a compiled plugin adds a
# module-graph requirement, and pluginsgen rewrites go.work itself. Either would leave a
# nominally successful build holding an unexplained dirty tree, so surface it immediately.
# A --dev-plugin build writes NEITHER: its lock is go.work.dev.sum, next to its workspace.
# The guard runs only inside a Git worktree (a git-less source export has no lineage).
if git rev-parse --git-dir >/dev/null 2>&1; then
  dirty=()
  for f in go.work go.work.sum; do
    git diff --quiet -- "$f" || dirty+=("$f")
  done
  if [ ${#dirty[@]} -gt 0 ]; then
    echo "bootstrap-charly: generation/build changed tracked ${dirty[*]}" >&2
    echo "Review and commit the workspace wiring, then re-run bootstrap-charly.sh." >&2
    git diff -- "${dirty[@]}" >&2
    rm -f bin/.charly.next
    exit 1
  fi
fi

mv bin/.charly.next bin/charly
if [ ${#dev_specs[@]} -gt 0 ]; then
  echo "bootstrap-charly: built ./bin/charly ($CALVER) — DEV BUILD, local plugin source"
else
  echo "bootstrap-charly: built ./bin/charly ($CALVER)"
fi

# --install: OPTIONAL portable install to $HOME/.local/bin (solo/bootstrap use only —
# can shadow a system charly if $HOME/.local/bin precedes /usr/bin in $PATH). Refused for
# a --dev-plugin build: this binary links UNMERGED plugin source, and a shared install is
# how a dev build would silently become everyone's charly.
if [ "$install" = 1 ]; then
  if [ ${#dev_specs[@]} -gt 0 ]; then
    echo "bootstrap-charly: --install refused for a --dev-plugin dev build" >&2
    exit 2
  fi
  install -D -m 0755 bin/charly "$HOME/.local/bin/charly"
  echo "bootstrap-charly: installed to $HOME/.local/bin/charly"
fi
