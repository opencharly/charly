package main

import (
	"github.com/opencharly/sdk/buildkit"
	"strings"
	"testing"
)

// TestSharedCacheMount_StableID locks in the format that makes BuildKit
// caches survive layer-hash churn — the entire reason CacheMount exists.
func TestSharedCacheMount_StableID(t *testing.T) {
	got := buildkit.SharedCacheMount("/var/cache/libdnf5", "").String()
	want := "--mount=type=cache,id=charly-var-cache-libdnf5,dst=/var/cache/libdnf5,sharing=locked"
	if got != want {
		t.Errorf("SharedCacheMount default sharing\n  got:  %s\n  want: %s", got, want)
	}

	got = buildkit.SharedCacheMount("/var/cache/pacman/pkg", "shared").String()
	want = "--mount=type=cache,id=charly-var-cache-pacman-pkg,dst=/var/cache/pacman/pkg,sharing=shared"
	if got != want {
		t.Errorf("SharedCacheMount nested path\n  got:  %s\n  want: %s", got, want)
	}
}

// TestOwnedCacheMount_UIDInID confirms uid is part of the id namespace so
// different-uid builds don't collide on file ownership inside the cache volume.
func TestOwnedCacheMount_UIDInID(t *testing.T) {
	got := buildkit.OwnedCacheMount("/tmp/pixi-cache", 1000, 1000).String()
	want := "--mount=type=cache,id=charly-tmp-pixi-cache-uid1000,dst=/tmp/pixi-cache,uid=1000,gid=1000"
	if got != want {
		t.Errorf("OwnedCacheMount\n  got:  %s\n  want: %s", got, want)
	}

	// Same dst, different uid → different id (the whole point).
	a := buildkit.OwnedCacheMount("/tmp/npm-cache", 1000, 1000).String()
	b := buildkit.OwnedCacheMount("/tmp/npm-cache", 2000, 2000).String()
	if a == b {
		t.Errorf("uid must differentiate the cache id; both produced:\n  %s", a)
	}
	if !strings.Contains(a, "uid1000") || !strings.Contains(b, "uid2000") {
		t.Errorf("expected uid suffix in id; got\n  a=%s\n  b=%s", a, b)
	}
}

// TestRenderCacheMountsAuto_Mixed locks in the per-entry owned/shared split:
// one builder (the AUR stage) declares the root pacman cache (shared/locked)
// alongside user-writable build caches (makepkg SRCDEST, yay clones — owned),
// and each renders in its correct form from a single list.
func TestRenderCacheMountsAuto_Mixed(t *testing.T) {
	mounts := []CacheMountDef{
		{Dst: "/var/cache/pacman/pkg", Sharing: "locked"}, // root system cache
		{Dst: "/tmp/aur-srcdest", Owned: true},            // user build cache
		{Dst: "/tmp/aur-xdg-cache", Owned: true},          // user build cache
	}
	out := buildkit.RenderCacheMountsAuto(mounts, 1000, 1000, " ", false)
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
	if got := buildkit.RenderCacheMounts(nil, -1, 0, " \\\n    ", true); got != "" {
		t.Errorf("empty mounts must produce empty string, got: %q", got)
	}
}

// TestRenderCacheMounts_TrailingSeparator covers the cacheMountsOwned shape
// where we need the separator after the last entry (template chains into RUN body).
func TestRenderCacheMounts_TrailingSeparator(t *testing.T) {
	mounts := []CacheMountDef{{Dst: "/tmp/pixi-cache"}}
	got := buildkit.RenderCacheMounts(mounts, 1000, 1000, " \\\n    ", true)
	if !strings.HasSuffix(got, " \\\n    ") {
		t.Errorf("trailing separator missing; got: %q", got)
	}
	if !strings.Contains(got, "id=charly-tmp-pixi-cache-uid1000") {
		t.Errorf("expected stable id; got: %q", got)
	}
}

// TestCacheMountID_StableAcrossInvocations is the core regression guard:
// the same dst MUST produce the same id every time, otherwise cache is
// keyed by something volatile and breaks the entire purpose of the fix.
func TestCacheMountID_StableAcrossInvocations(t *testing.T) {
	for i := range 10 {
		a := buildkit.SharedCacheMount("/var/cache/libdnf5", "locked").String()
		b := buildkit.SharedCacheMount("/var/cache/libdnf5", "locked").String()
		if a != b {
			t.Fatalf("non-deterministic id at iteration %d:\n  a=%s\n  b=%s", i, a, b)
		}
	}
}
