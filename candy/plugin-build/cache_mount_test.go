package build

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
)

// Relocated from charly/cache_mount_test.go (#55 decoupling cone, Batch B) — these three tests
// assert buildkit.SharedCacheMount / OwnedCacheMount directly, zero charly coupling. The
// spec.RenderCacheMounts*-facing tests stayed in charly (spec.CacheMount is a plain spec type).

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
