package spec

import (
	"fmt"
	"strings"
)

// cache_mount_render.go — the cache-mount → Containerfile `--mount=type=cache,…`
// flag render primitives (#55 coneK3tasks, relocated from sdk/buildkit/render.go). Pure
// string formatting over the authoring spec.CacheMount (dst/sharing/owned) + a concrete
// uid/gid — no *ResolvedBox, no *Generator, no buildkit/deploykit import. The shared SINGLE
// source (R3) for every cache-mount render site — charly core (the inline-builder seam),
// sdk/buildkit (the format/builder template funcs + the single-mount constructors), and
// sdk/deploykit (the bootstrap + the cmd-emitter) — so charly can shed its sdk/deploykit
// import without gaining a sdk/buildkit one (charly core imports neither sdk kit). The
// cone-render precedent (render_seam.go, relocated from sdk/deploykit for the SAME
// import-purity reason) is the template: a render primitive lands in spec/spec so the host
// and the plugins share ONE source with no kit import.
//
// buildkit's own render-time CacheMount/OwnedCacheMount/SharedCacheMount stay in buildkit as
// constructors whose .String() delegates to FormatCacheMount here — one formatter, no
// duplicate (the slice renderers below call FormatCacheMount per entry; buildkit's
// .String() calls it for a single mount).

// FormatCacheMount renders ONE cache mount as a Containerfile `--mount=type=cache,…` flag.
// uid>=0 → the owned form (uid/gid baked into the id namespace so different-uid builds don't
// collide on file ownership inside the cache volume); uid<0 → the shared form (sharing mode,
// defaulting to "locked" when empty — the BuildKit default for root-installed system caches).
// The id is derived from dst (and uid for owned), keeping the cache stable across layer-hash
// churn during iterative builds — the entire reason a derived id exists.
func FormatCacheMount(dst, sharing string, uid, gid int) string {
	safe := strings.ReplaceAll(strings.TrimPrefix(dst, "/"), "/", "-")
	id := "charly-" + safe
	if uid >= 0 {
		return fmt.Sprintf("--mount=type=cache,id=%s-uid%d,dst=%s,uid=%d,gid=%d", id, uid, dst, uid, gid)
	}
	if sharing == "" {
		sharing = "locked"
	}
	return fmt.Sprintf("--mount=type=cache,id=%s,dst=%s,sharing=%s", id, dst, sharing)
}

// RenderCacheMounts joins a slice of spec.CacheMount into one Containerfile flag string.
// uid<0 → shared form (sharing-locked); uid>=0 → owned form. `trailing` appends the
// separator after the last entry — needed by cacheMountsOwned which feeds directly into a
// multi-line RUN body. Single source of truth for the slice-rendering pattern that
// previously lived inline at four call sites (two template helpers + the former generate.go
// + tasks.go cmd-emitter); every multi-mount site now flows through here, every single-mount
// site flows through FormatCacheMount (via buildkit's CacheMount.String()) directly.
func RenderCacheMounts(mounts []CacheMount, uid, gid int, sep string, trailing bool) string {
	if len(mounts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if uid >= 0 {
			parts = append(parts, FormatCacheMount(m.Dst, m.Sharing, uid, gid))
		} else {
			parts = append(parts, FormatCacheMount(m.Dst, m.Sharing, -1, 0))
		}
	}
	out := strings.Join(parts, sep)
	if trailing {
		out += sep
	}
	return out
}

// RenderCacheMountsAuto renders a MIXED list where each entry is owned (uid/gid) or shared
// per its own `owned:` flag — letting one builder declare both root system caches
// (pacman → shared/locked) and user build caches (makepkg SRCDEST, yay AUR clones →
// uid/gid-owned) in a single cache_mount list. uid/gid apply only to the entries flagged
// owned; the rest render in the shared form.
func RenderCacheMountsAuto(mounts []CacheMount, uid, gid int, sep string, trailing bool) string {
	if len(mounts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if m.Owned {
			parts = append(parts, FormatCacheMount(m.Dst, m.Sharing, uid, gid))
		} else {
			parts = append(parts, FormatCacheMount(m.Dst, m.Sharing, -1, 0))
		}
	}
	out := strings.Join(parts, sep)
	if trailing {
		out += sep
	}
	return out
}
