#!/usr/bin/env bash
# pkg-install-test.sh — install-test the built charly native packages (.pkg.tar.zst /
# .rpm / .deb in a dist dir) on every maintainer-supported distro version, in
# throwaway rootless-podman containers. This is the SINGLE canonical definition of
# the package test matrix (R3): `task pkg:test` runs it locally and the
# release-packages workflow calls the SAME script per format in CI — never a second
# inline copy.
#
# The matrix (verified against upstream maintainer support 2026-07; see
# taskfiles/Build.yml "Package test matrix" for the version-bump runbook):
#
#   cell            base image                                    artifact
#   pac-arch        docker.io/library/archlinux:latest            .pkg.tar.zst
#   pac-cachyos     cachyos base ref read from THIS repo's own pin (box/cachyos/box/cachyos/charly.yml)
#   deb-debian13    docker.io/library/debian:13                   .deb
#   deb-ubuntu2404  docker.io/library/ubuntu:24.04                .deb
#   deb-ubuntu2604  docker.io/library/ubuntu:26.04                .deb
#   rpm-fedora43    docker.io/library/fedora:43                   .rpm
#   rpm-fedora44    docker.io/library/fedora:44                   .rpm
#
# Each cell runs ONE container that: refreshes the package metadata, natively
# installs the artifact (auto-resolving every mandatory dep from the distro's own
# repos), asserts package-manager registration of opencharly + a dep spot-check set,
# asserts `charly version` equals the expected CalVer, and asserts the welded
# command plugins dispatch project-less (`charly doctor` from /tmp).
#
# Usage:  scripts/pkg-install-test.sh <dist-dir> <expected-calver> [cell ...]
#         LOGDIR=<dir>  — where per-cell logs go (default: mktemp -d, printed)
# Exit:   0 only when every selected cell prints CELL-OK.
set -u

if [ $# -lt 2 ]; then
    echo "usage: $0 <dist-dir> <expected-calver> [cell ...]" >&2
    exit 2
fi
DIST=$(cd "$1" && pwd)
WANT_VER=$2
shift 2
LOGDIR=${LOGDIR:-$(mktemp -d /tmp/pkg-install-test-logs.XXXXXX)}
mkdir -p "$LOGDIR"
echo "logs: $LOGDIR"

# The cachyos cell tracks the repo's OWN pinned cachyos base ref (R3 — never a
# second hardcoded digest here). Requires the box/cachyos submodule.
CACHYOS_REF=$(grep -m1 'base: docker.io/cachyos/cachyos-v3@' box/cachyos/box/cachyos/charly.yml 2>/dev/null | awk '{print $2}')
if [ -z "$CACHYOS_REF" ]; then
    echo "error: cannot read the cachyos base ref from box/cachyos/box/cachyos/charly.yml — initialize the submodule: git submodule update --init box/cachyos" >&2
    exit 2
fi

pac_install() { cat <<'EOF'
(pacman-key --populate 2>/dev/null || true) && pacman -Sy --noconfirm && pacman -U --noconfirm $(ls -1 /dist/*.pkg.tar.zst | grep -v -- '-debug')
EOF
}
deb_install() { cat <<'EOF'
export DEBIAN_FRONTEND=noninteractive && apt-get update -qq && apt-get install -y /dist/*.deb
EOF
}
rpm_install() { cat <<'EOF'
dnf install -y /dist/*.rpm
EOF
}

# run_cell <name> <image> <install-cmd> <query-cmd> <space-separated pkgs>
run_cell() {
    local name=$1 image=$2 install=$3 query=$4 pkgs=$5
    local log="$LOGDIR/$name.log"
    echo "=== CELL $name ($image)"
    local probe="" p
    for p in $pkgs; do
        probe="$probe $query $p >/dev/null && echo \"  REG-OK $p\" &&"
    done
    podman run --rm -v "$DIST:/dist:ro" "$image" bash -lc "
        set -e
        $install
        echo INSTALL-OK
        $probe true
        v=\$(charly version)
        [ \"\$v\" = \"$WANT_VER\" ] || { echo \"VERSION-MISMATCH got=\$v want=$WANT_VER\"; exit 1; }
        echo \"VERSION-OK \$v\"
        test -x /usr/lib/charly/plugins/plugin-doctor
        test -f /usr/lib/charly/plugins/plugin-doctor.providers
        test -x /usr/lib/charly/plugins/plugin-vm
        test -x /usr/lib/charly/plugins/plugin-clean
        cd /tmp && charly doctor 2>/dev/null | grep -q 'Container Engine'
        echo WELDED-CMD-PROJECT-LESS-OK
        echo CELL-OK
    " >"$log" 2>&1
    local rc=$?
    if [ $rc -ne 0 ] || ! grep -q '^CELL-OK$' "$log"; then
        echo "FAIL $name (exit $rc) — see $log"
        tail -15 "$log"
        return 1
    fi
    echo "PASS $name ($(grep -o 'VERSION-OK .*' "$log" | cut -d' ' -f2))"
}

cells="${@:-pac-arch pac-cachyos deb-debian13 deb-ubuntu2404 deb-ubuntu2604 rpm-fedora43 rpm-fedora44}"
fail=0
for c in $cells; do
    case $c in
        pac-arch)       run_cell "$c" docker.io/library/archlinux:latest "$(pac_install)" "pacman -Q" "opencharly-git podman libvirt tailscale gocryptfs" || fail=1 ;;
        pac-cachyos)    run_cell "$c" "$CACHYOS_REF" "$(pac_install)" "pacman -Q" "opencharly-git podman libvirt tailscale gocryptfs" || fail=1 ;;
        deb-debian13)   run_cell "$c" docker.io/library/debian:13 "$(deb_install)" "dpkg -s" "opencharly podman libvirt-daemon-system gocryptfs" || fail=1 ;;
        deb-ubuntu2404) run_cell "$c" docker.io/library/ubuntu:24.04 "$(deb_install)" "dpkg -s" "opencharly podman libvirt-daemon-system gocryptfs" || fail=1 ;;
        deb-ubuntu2604) run_cell "$c" docker.io/library/ubuntu:26.04 "$(deb_install)" "dpkg -s" "opencharly podman libvirt-daemon-system gocryptfs" || fail=1 ;;
        rpm-fedora43)   run_cell "$c" docker.io/library/fedora:43 "$(rpm_install)" "rpm -q" "opencharly podman libvirt tailscale gocryptfs" || fail=1 ;;
        rpm-fedora44)   run_cell "$c" docker.io/library/fedora:44 "$(rpm_install)" "rpm -q" "opencharly podman libvirt tailscale gocryptfs" || fail=1 ;;
        *) echo "unknown cell: $c" >&2; fail=1 ;;
    esac
done
echo "== overall: $([ $fail -eq 0 ] && echo ALL-PASS || echo FAILURES)"
exit $fail
