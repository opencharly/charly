# shellcheck shell=bash
# charly_calver — the CalVer (YYYY.DDD.HHMM, UTC) that identifies a charly build.
#
# Single source of truth for the build-time version stamp, shared (R3) by:
#   - pkg/arch/PKGBUILD   — pkgver() (pacman package version) + build() ldflags
#   - taskfiles/Build.yml — `task build:binary` ldflags injection
#
# Injected into the binary via `-ldflags "-X main.BuildCalVer=$(charly_calver)"` so
# `charly version` reports a FROZEN, DETERMINISTIC build identity. The CalVer is
# derived ONLY from the HEAD commit's UTC date — so EVERY binary built from the
# same commit reports the IDENTICAL version: a dirty working-tree `task
# build:binary`, the clean `git+file://` makepkg clone its `pkgver()` reads, and an
# AUR build all agree. The build clock is never consulted: the wall clock
# identifies the MOMENT of a build, not its SOURCE, and that conflation is what
# makes two builds of one commit disagree (e.g. `pacman` pkgver vs `charly version`)
# and lets a stale binary falsely sort "newer" than a fresh one. The format
# matches ComputeCalVerAt() in charly/version.go exactly — CANONICAL fixed-width:
# YYYY = 4-digit year, DDD = 3-digit zero-padded day-of-year, HHMM = 4-digit
# zero-padded hour*100 + minute, all UTC. Fixed-width so a plain alphanumeric
# sort of CalVer strings is chronological.
charly_calver() {
	local y d h m
	if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		echo "charly_calver: not inside a git work tree — cannot derive a deterministic CalVer (build charly from its git checkout)" >&2
		return 1
	fi
	# HEAD commit's UTC timestamp — identical for a clean tree and a dirty tree at
	# the same commit, so the stamp identifies the SOURCE, never the build moment.
	read -r y d h m <<<"$(TZ=UTC0 git log -1 --format=%cd --date='format-local:%Y %j %H %M')"
	printf '%04d.%03d.%04d\n' "$((10#$y))" "$((10#$d))" "$(((10#$h) * 100 + (10#$m)))"
}

# Direct execution (`bash calver.sh`) prints the value — convenient for the
# Taskfile to capture in a $(...) without sourcing into its own shell.
if [ "${BASH_SOURCE[0]:-$0}" = "$0" ]; then
	charly_calver
fi
