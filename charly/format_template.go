package main

import (
	"fmt"
	"strings"
	"text/template"
)

// CacheMount is the RENDER-time cache-mount value (a resolved Containerfile
// `--mount=type=cache,…` directive). Distinct from the authoring
// spec.CacheMount (CacheMountDef — dst/sharing/owned): this one carries the
// concrete uid/gid the owned form resolved to (sentinel UID == -1 = the shared
// form). Produced by SharedCacheMount/OwnedCacheMount from the authoring def.
type CacheMount struct {
	Dst     string // Container-side mount path; canonical id source.
	Sharing string // "locked"/"shared"/"private" for shared caches; ignored for owned.
	UID     int    // For owned caches; sentinel -1 means "shared" form.
	GID     int
}

// SharedCacheMount returns a cache-mount value for root-installed system
// caches (dnf/apt/pacman/downloads). Sharing defaults to "locked".
func SharedCacheMount(dst, sharing string) CacheMount {
	if sharing == "" {
		sharing = "locked"
	}
	return CacheMount{Dst: dst, Sharing: sharing, UID: -1}
}

// OwnedCacheMount returns a cache-mount value for non-root user caches
// (pixi/npm/cargo). UID becomes part of the id namespace.
func OwnedCacheMount(dst string, uid, gid int) CacheMount {
	return CacheMount{Dst: dst, UID: uid, GID: gid}
}

// String renders the CacheMount as a Containerfile `--mount=type=cache,...`
// flag. The `id=` field is derived from Dst (and UID for owned caches), keeping
// the cache stable across layer-hash changes during iterative builds.
func (m CacheMount) String() string {
	safe := strings.ReplaceAll(strings.TrimPrefix(m.Dst, "/"), "/", "-")
	id := "charly-" + safe
	if m.UID >= 0 {
		return fmt.Sprintf("--mount=type=cache,id=%s-uid%d,dst=%s,uid=%d,gid=%d", id, m.UID, m.Dst, m.UID, m.GID)
	}
	return fmt.Sprintf("--mount=type=cache,id=%s,dst=%s,sharing=%s", id, m.Dst, m.Sharing)
}

// RenderCacheMounts joins a slice of CacheMountDef into one Containerfile
// flag string. uid<0 → shared form (sharing-locked); uid>=0 → owned form.
// `trailing` appends the separator after the last entry — needed by
// `cacheMountsOwned` which feeds directly into a multi-line RUN body.
//
// Single source of truth for the slice-rendering pattern that previously
// lived inline at four call sites (two template helpers + generate.go +
// tasks.go cmd-emitter). Every multi-mount site now flows through here,
// every single-mount site flows through CacheMount.String() directly.
func RenderCacheMounts(mounts []CacheMountDef, uid, gid int, sep string, trailing bool) string {
	if len(mounts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if uid >= 0 {
			parts = append(parts, OwnedCacheMount(m.Dst, uid, gid).String())
		} else {
			parts = append(parts, SharedCacheMount(m.Dst, m.Sharing).String())
		}
	}
	out := strings.Join(parts, sep)
	if trailing {
		out += sep
	}
	return out
}

// RenderCacheMountsAuto renders a MIXED list where each entry is owned
// (uid/gid) or shared per its own `owned:` flag — letting one builder declare
// both root system caches (pacman → shared/locked) and user build caches
// (makepkg SRCDEST, yay AUR clones → uid/gid-owned) in a single cache_mount
// list. uid/gid apply only to the entries flagged owned.
func RenderCacheMountsAuto(mounts []CacheMountDef, uid, gid int, sep string, trailing bool) string {
	if len(mounts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if m.Owned {
			parts = append(parts, OwnedCacheMount(m.Dst, uid, gid).String())
		} else {
			parts = append(parts, SharedCacheMount(m.Dst, m.Sharing).String())
		}
	}
	out := strings.Join(parts, sep)
	if trailing {
		out += sep
	}
	return out
}

// templateFuncs provides helper functions for format/builder templates.
var templateFuncs = template.FuncMap{
	"cacheMounts":      func(m []CacheMountDef) string { return RenderCacheMounts(m, -1, 0, " \\\n    ", false) },
	"cacheMountsOwned": func(m []CacheMountDef, uid, gid int) string { return RenderCacheMounts(m, uid, gid, " \\\n    ", true) },
	"cacheMountsAuto": func(m []CacheMountDef, uid, gid int) string {
		return RenderCacheMountsAuto(m, uid, gid, " \\\n    ", false)
	},

	// quote returns a shell-safe quoted string.
	"quote": func(s any) string {
		return fmt.Sprintf("%q", fmt.Sprint(s))
	},

	// default returns the value if non-empty, otherwise the fallback.
	"default": func(val, fallback any) any {
		s := fmt.Sprint(val)
		if s == "" || s == "<nil>" {
			return fallback
		}
		return val
	},

	// splitFirst splits a string by sep and returns the first part.
	"splitFirst": func(s, sep string) string {
		parts := strings.SplitN(s, sep, 2)
		return parts[0]
	},

	// replace performs string replacement.
	"replace": strings.ReplaceAll,

	// join joins a string slice with a separator.
	"join": func(elems any, sep string) string {
		switch v := elems.(type) {
		case []string:
			return strings.Join(v, sep)
		case []any:
			strs := make([]string, len(v))
			for i, e := range v {
				strs[i] = fmt.Sprint(e)
			}
			return strings.Join(strs, sep)
		default:
			return fmt.Sprint(elems)
		}
	},

	// printf is a template-accessible Sprintf.
	"printf": fmt.Sprintf,

	// shquote shell-quotes a single argument (delegates to shQuoteArg), so a
	// host-venue builder/install template can emit a package or path argument
	// safely. Used by the aur builder's phase.install.host cell to quote each
	// AUR package name passed to yay.
	"shquote": shQuoteArg,

	// hasSuffix reports whether a string ends with the given suffix.
	// Used by the rpm install template to distinguish a URL pointing at
	// a `.repo` configuration file (consumable by `dnf5 config-manager
	// addrepo --from-repofile`) from a yum baseurl that needs an
	// inline `.repo` file generated locally.
	"hasSuffix": strings.HasSuffix,

	// anyRepoHasURL reports whether any repo entry declares a `url` key
	// (i.e. needs `dnf5 config-manager addrepo`). Lets install_template
	// conditionally install `dnf5-plugins` — necessary on bootc bases
	// which strip it from the default install.
	"anyRepoHasURL": func(repos []map[string]any) bool {
		for _, r := range repos {
			if u, ok := r["url"]; ok && fmt.Sprint(u) != "" {
				return true
			}
		}
		return false
	},
}

// InstallContext + BuildStageContext are the format/builder render-context
// keystones; they are CUE-sourced in sdk/schema/buildctx.cue and generated into
// sdk/spec by `task cue:gen`, aliased back via vmshared_aliases.go
// (BuildStageContext / InstallContext), so a builder plugin can consume them over
// OpResolve. The render/constructor funcs below operate on the aliased types
// unchanged.

// RenderTemplate renders a Go text/template with the given context.
func RenderTemplate(name, tmplStr string, ctx any) (string, error) {
	if tmplStr == "" {
		return "", nil
	}
	tmpl, err := template.New(name).Funcs(templateFuncs).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parsing template %q: %w", name, err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, ctx); err != nil {
		return "", fmt.Errorf("executing template %q: %w", name, err)
	}
	return b.String(), nil
}

// NewInstallContext creates an InstallContext from a generic PackageSection.
func NewInstallContext(section map[string]any, cacheMounts []CacheMountDef) *InstallContext {
	ctx := &InstallContext{
		CacheMounts: cacheMounts,
	}

	if pkgs, ok := section["package"]; ok {
		ctx.Packages = toStringSlice(pkgs)
	}
	if repos, ok := section["repo"]; ok {
		ctx.Repos = toMapSlice(repos)
	}
	if opts, ok := section["options"]; ok {
		ctx.Options = toStringSlice(opts)
	}
	if copr, ok := section["copr"]; ok {
		ctx.Copr = toStringSlice(copr)
	}
	if mods, ok := section["module"]; ok {
		ctx.Modules = toStringSlice(mods)
	}
	if excl, ok := section["exclude"]; ok {
		ctx.Exclude = toStringSlice(excl)
	}
	if keys, ok := section["keys"]; ok {
		ctx.Keys = toStringSlice(keys)
	}

	return ctx
}

// toStringSlice converts an interface{} to []string.
func toStringSlice(v any) []string {
	switch val := v.(type) {
	case []string:
		return val
	case []any:
		result := make([]string, len(val))
		for i, e := range val {
			result[i] = fmt.Sprint(e)
		}
		return result
	default:
		return nil
	}
}

// toMapSlice converts an interface{} to []map[string]interface{}. Accepts
// both `[]interface{}` (legacy raw-YAML decode shape) and `[]map[string]any`
// (typed shape produced by the post-2026-05 derivePackageSectionsFromCalamares
// bridge that copies `DistroPackages.Repos` directly into PackageSection.Raw).
func toMapSlice(v any) []map[string]any {
	switch val := v.(type) {
	case []any:
		result := make([]map[string]any, 0, len(val))
		for _, e := range val {
			if m, ok := e.(map[string]any); ok {
				result = append(result, m)
			}
		}
		return result
	case []map[string]any:
		// Already the right shape (also matches []map[string]any since
		// `any` is an alias for `interface{}`); just copy.
		result := make([]map[string]any, len(val))
		copy(result, val)
		return result
	default:
		return nil
	}
}
