#!/usr/bin/env bash
# scripts/check-repo-cache.sh — the ONE implementation behind every arm of the
# check-repo-cache-local bed.
#
# The bed proves that the `--repo` resolver reads ONE cache root, and that each of
# the three ways of naming that root points at the same store:
#
#   the XDG default            $XDG_CACHE_HOME/charly/repos/<repo>@<ref>
#   CHARLY_CACHE_DIR           <dir>/repos/<repo>@<ref>
#   CHARLY_REPO_CACHE          the repos directory ITSELF (the kept override)
#
# Every arm seeds ONE immutable entry in a fresh root and asserts the resolver
# resolves it WITHOUT a clone. The fixture repo does not exist anywhere, so a
# PASS can only mean the seeded entry answered — and to make that claim
# deterministic the whole bed runs with every `https://github.com/` clone URL
# rewritten to a missing local path, so a cache miss fails immediately instead of
# reaching the network (and instead of hanging on a credential prompt). The
# `miss` arm proves the rewrite is in force: an EMPTY root must fail with the
# missing-remote error.
#
#   check-repo-cache.sh <arm> <charly-binary>
#
#   xdg        the seeded entry under the XDG cache root
#   cache-dir  the same entry reached through CHARLY_CACHE_DIR
#   legacy     the same entry reached through CHARLY_REPO_CACHE
#   miss       CONTROL: an empty root attempts the clone and fails offline
#
# The binary is an ARGUMENT, not an environment variable: a check step's command text
# has `${...}` expanded by the runner before the shell sees it, so a bare env var would
# be empty inside this script (measured: exit 1, "CHARLY_BIN must name the charly binary").
set -eu

arm=${1:?usage: check-repo-cache.sh <xdg|cache-dir|legacy|miss> <charly-binary>}
bin=${2:?usage: check-repo-cache.sh <arm> <charly-binary>}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Test isolation: every github.com clone is rewritten to a local path that does not
# exist, so a cache miss fails at once and never reaches the network. GIT_TERMINAL_PROMPT
# keeps a credential prompt from turning a miss into a hang.
export GIT_TERMINAL_PROMPT=0
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=url.file:///nonexistent-remote.insteadof
export GIT_CONFIG_VALUE_0=https://github.com/

# seed <repos-dir> — one immutable entry, with the provenance that answers the
# version question without a fetch.
seed() {
    entry="$1/github.com/opencharly/fixture@v1.0.0"
    mkdir -p "$entry/box" "$entry/candy"
    printf 'discover: [box, candy]\n' > "$entry/charly.yml"
    printf '{"version":2,"commit":"v1.0.0"}\n' > "$entry.ref"
}

# resolve <label> <env assignment>... — resolve the seeded ref and assert the
# answer came from the entry, not from a clone.
resolve() {
    label=$1
    shift
    if ! out=$(env "$@" "$bin" --repo opencharly/fixture@v1.0.0 version 2>&1); then
        printf '%s\n' "$out"
        printf 'the resolver failed under %s\n' "$label" >&2
        exit 1
    fi
    printf '%s\n' "$out"
    case "$out" in
    *"Cloning into"* | *"fatal:"*)
        printf 'the resolver MISSED the seeded entry for %s (a clone was attempted)\n' "$label" >&2
        exit 1
        ;;
    esac
    printf 'resolved without a clone through %s\n' "$label"
}

case "$arm" in
xdg)
    seed "$tmp/charly/repos"
    resolve "the XDG cache root" XDG_CACHE_HOME="$tmp" CHARLY_CACHE_DIR=
    ;;
cache-dir)
    seed "$tmp/repos"
    resolve "CHARLY_CACHE_DIR" XDG_CACHE_HOME= CHARLY_CACHE_DIR="$tmp"
    ;;
legacy)
    seed "$tmp/legacy"
    mkdir -p "$tmp/empty-cache"
    resolve "CHARLY_REPO_CACHE" XDG_CACHE_HOME="$tmp/empty-cache" CHARLY_CACHE_DIR= \
        CHARLY_REPO_CACHE="$tmp/legacy"
    ;;
miss)
    mkdir -p "$tmp/empty"
    if out=$(env XDG_CACHE_HOME="$tmp/empty" CHARLY_CACHE_DIR= CHARLY_REPO_CACHE= \
        "$bin" --repo opencharly/fixture@v1.0.0 version 2>&1); then
        printf '%s\n' "$out"
        printf 'the resolver answered from an EMPTY cache root: the entry is not what answered\n' >&2
        exit 1
    fi
    printf '%s\n' "$out"
    case "$out" in
    *"does not appear to be a git repository"* | *"Could not read from remote repository"*)
        printf 'CONTROL: an empty root attempts a clone and fails offline, so the seeded arms cannot pass by cloning\n'
        ;;
    *)
        printf 'the empty root failed without the offline clone attempt; the control does not discriminate\n' >&2
        exit 1
        ;;
    esac
    ;;
*)
    printf 'unknown arm: %s\n' "$arm" >&2
    exit 2
    ;;
esac
