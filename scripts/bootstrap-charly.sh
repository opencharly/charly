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
#
# Usage: run from the repo root (any worktree of a charly checkout).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p bin

# Regenerate the compiled-in plugin wiring from charly.yml `compiled_plugins:` BEFORE
# the go build that needs it: pluginsgen emits charly/plugins_generated.go
# (registerCompiledPlugin per selected plugin candy) + the repo-root go.work. GOWORK=off
# so a stale go.work can't fail workspace load before regeneration; the generator
# imports only stdlib+yaml and runs without a pre-built charly.
echo "bootstrap-charly: regenerating compiled-in plugin wiring"
(cd charly && GOWORK=off go run ./internal/pluginsgen \
  -root .. -config charly/charly.yml \
  -out charly/plugins_generated.go -gowork go.work)

# Stamp the binary's CalVer identity (`charly version` -> main.BuildCalVer) at build
# time, from the shared scripts/calver.sh — ALWAYS the HEAD commit's UTC date
# (deterministic: same commit -> same version, clean or dirty). Without this stamp
# `charly version` would read the wall clock at invocation.
# Build to a temp path FIRST so a guard failure below can never leave a half-written
# bin/charly in place; promotion happens only after every check passes.
# -buildvcs=false: every charly binary build passes it (the VCS stamp has zero
# consumers, and workspace-mode Go's VCS-status walk breaks in a linked worktree
# outside the main repo path).
echo "bootstrap-charly: building bin/charly"
CALVER="$(bash scripts/calver.sh)"
(cd charly && GOWORK="$ROOT/go.work" go build -buildvcs=false \
  -ldflags "-X main.BuildCalVer=${CALVER}" -o ../bin/.charly.next .)

# Workspace-mode Go may extend the tracked go.work.sum when a compiled plugin adds a
# module-graph requirement. Surface that generated metadata immediately instead of
# letting a nominally successful build leave an unexplained dirty tree. The guard
# runs only inside a Git worktree (a git-less source export has no checksum lineage).
if git rev-parse --git-dir >/dev/null 2>&1 && ! git diff --quiet -- go.work.sum; then
  echo "bootstrap-charly: go build updated tracked go.work.sum" >&2
  echo "Review and commit the workspace checksum, then re-run bootstrap-charly.sh." >&2
  git diff -- go.work.sum >&2
  rm -f bin/.charly.next
  exit 1
fi

mv bin/.charly.next bin/charly
echo "bootstrap-charly: built ./bin/charly ($CALVER)"

# --install: OPTIONAL portable install to $HOME/.local/bin (solo/bootstrap use only —
# can shadow a system charly if $HOME/.local/bin precedes /usr/bin in $PATH).
if [ "${1:-}" = "--install" ]; then
  install -D -m 0755 bin/charly "$HOME/.local/bin/charly"
  echo "bootstrap-charly: installed to $HOME/.local/bin/charly"
fi
