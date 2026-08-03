package main

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestSharedCacheMount_StableID / TestOwnedCacheMount_UIDInID / TestCacheMountID_StableAcrossInvocations
// (buildkit.SharedCacheMount / OwnedCacheMount direct assertions) moved to
// candy/plugin-build/cache_mount_test.go (#55 decoupling cone, Batch B).

// TestRenderCacheMountsAuto_Mixed locks in the per-entry owned/shared split:
// one builder (the AUR stage) declares the root pacman cache (shared/locked)
// alongside user-writable build caches (makepkg SRCDEST, yay clones — owned),
// and each renders in its correct form from a single list.
func TestRenderCacheMountsAuto_Mixed(t *testing.T) {
	mounts := []spec.CacheMount{
		{Dst: "/var/cache/pacman/pkg", Sharing: "locked"}, // root system cache
		{Dst: "/tmp/aur-srcdest", Owned: true},            // user build cache
		{Dst: "/tmp/aur-xdg-cache", Owned: true},          // user build cache
	}
	out := spec.RenderCacheMountsAuto(mounts, 1000, 1000, " ", false)
	if !strings.Contains(out, "id=charly-var-cache-pacman-pkg,dst=/var/cache/pacman/pkg,sharing=locked") {
		t.Errorf("pacman entry must stay shared/locked (no uid):\n%s", out)
	}
	if strings.Contains(out, "pacman-pkg-uid") {
		t.Errorf("pacman entry must NOT be uid-owned:\n%s", out)
	}
	if !strings.Contains(out, "id=charly-tmp-aur-srcdest-uid1000,dst=/tmp/aur-srcdest,uid=1000,gid=1000") {
		t.Errorf("srcdest entry must be uid-owned:\n%s", out)
	}
	if !strings.Contains(out, "id=charly-tmp-aur-xdg-cache-uid1000,dst=/tmp/aur-xdg-cache,uid=1000,gid=1000") {
		t.Errorf("xdg-cache entry must be uid-owned:\n%s", out)
	}
}

// TestRenderCacheMounts_Empty must NOT emit a trailing separator when the
// slice is empty — otherwise generated Containerfiles get a stray `\` line.
func TestRenderCacheMounts_Empty(t *testing.T) {
	if got := spec.RenderCacheMounts(nil, -1, 0, " \\\n    ", true); got != "" {
		t.Errorf("empty mounts must produce empty string, got: %q", got)
	}
}

// TestRenderCacheMounts_TrailingSeparator covers the cacheMountsOwned shape
// where we need the separator after the last entry (template chains into RUN body).
func TestRenderCacheMounts_TrailingSeparator(t *testing.T) {
	mounts := []spec.CacheMount{{Dst: "/tmp/pixi-cache"}}
	got := spec.RenderCacheMounts(mounts, 1000, 1000, " \\\n    ", true)
	if !strings.HasSuffix(got, " \\\n    ") {
		t.Errorf("trailing separator missing; got: %q", got)
	}
	if !strings.Contains(got, "id=charly-tmp-pixi-cache-uid1000") {
		t.Errorf("expected stable id; got: %q", got)
	}
}
