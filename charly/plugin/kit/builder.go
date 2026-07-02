package kit

// builder.go — the SINGLE shared implementation of the four detection-builders'
// DEPLOY-TIME IR shim (cargo / npm / pixi / aur), R3. Each builder is served by its OWN
// composable plugin candy (candy/plugin-builder-<word>), but all four dispatch the SAME two
// pure functions here, keyed by the builder word — so the per-builder Go logic lives in ONE
// place a leaf plugin module imports (this package depends only on the stdlib + charly/spec).
//
// A builder's BUILD-TIME multi-stage is resolved by kit.BuilderResolve (builder_resolve.go, C10);
// these functions are the deploy-time legs:
//
//   - BuilderCollectContext: the per-candy stage-context keys the host merges onto the base
//     ({layer,builder,home}) to form BuilderStep.RawStageContext. Behaviour-preserving copy of
//     the former in-proc CollectContext bodies (pixi → constant default env; aur → its section
//     packages + replaces; cargo/npm → none, refined host-side at install time).
//   - BuilderReverse: the teardown ops for a resolved stage context. The builder-specific
//     reverse-op KIND (pixi-env-remove / npm-uninstall-g / cargo-uninstall / package-remove) is
//     exactly the logic this externalization moves out-of-process. For aur the host fills the
//     package-remove UninstallCmd later (fillReverseUninstallCmds), so only Kind/Format/Targets/
//     Scope are named here.

import "github.com/opencharly/charly/charly/spec"

// BuilderCollectContext returns the builder-specific stage-context keys for `word` given the
// host-supplied candy descriptor. An unknown word returns nil (a custom candy builder with no
// plugin keeps base-only context — the host never invokes a plugin for it).
func BuilderCollectContext(word string, in spec.BuilderCollectInput) map[string]any {
	switch word {
	case "pixi":
		// Default pixi env name; a candy's pixi.toml [workspace]/[project] name overrides it,
		// which the host target reads at install time (behaviour-preserving with the former
		// pixiDefaultEnvName → "default").
		return map[string]any{"env_name": "default"}
	case "aur":
		ctx := map[string]any{}
		if len(in.Packages) > 0 {
			ctx["packages"] = in.Packages
		}
		if len(in.Replaces) > 0 {
			ctx["replaces"] = in.Replaces
		}
		return ctx
	case "npm", "cargo":
		// Globals (npm) / binaries (cargo) are read from package.json / Cargo.toml host-side at
		// install time (best-effort) — nothing derivable from the candy manifest alone.
		return nil
	}
	return nil
}

// BuilderReverse returns the teardown ops for `word` given its resolved stage context (the
// BuilderCollectContext output the host stored on the BuilderStep). An unknown word, or a
// context missing the keys a builder needs, returns nil (no teardown — the same best-effort the
// in-proc builders had).
func BuilderReverse(word string, in spec.BuilderReverseInput) []spec.ReverseOp {
	switch word {
	case "pixi":
		if env := builderCtxString(in.Context, "env_name"); env != "" {
			return []spec.ReverseOp{{
				Kind:    spec.ReverseOpPixiEnvRemove,
				Targets: []string{env},
				Scope:   spec.ScopeUser,
				Extra:   map[string]string{"layer": in.Candy},
			}}
		}
	case "npm":
		if pkgs := builderCtxStringSlice(in.Context, "packages"); len(pkgs) > 0 {
			return []spec.ReverseOp{{
				Kind:    spec.ReverseOpNpmUninstallG,
				Targets: pkgs,
				Scope:   spec.ScopeUser,
			}}
		}
	case "cargo":
		if bins := builderCtxStringSlice(in.Context, "binaries"); len(bins) > 0 {
			return []spec.ReverseOp{{
				Kind:    spec.ReverseOpCargoUninstall,
				Targets: bins,
				Scope:   spec.ScopeUser,
			}}
		}
	case "aur":
		// aur packages install into the host package DB; reverse is a package-remove (the host
		// renders UninstallCmd from the format's uninstall_template via fillReverseUninstallCmds).
		if pkgs := builderCtxStringSlice(in.Context, "packages"); len(pkgs) > 0 {
			return []spec.ReverseOp{{
				Kind:    spec.ReverseOpPackageRemove,
				Format:  "pac",
				Targets: pkgs,
				Scope:   spec.ScopeSystem,
			}}
		}
	}
	return nil
}

// builderCtxString reads a string value from a stage-context map (the JSON-decoded form, where
// values arrive as `any`). Empty when absent or not a string.
func builderCtxString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// builderCtxStringSlice reads a []string value from a stage-context map. Handles both a native
// []string and the JSON-decoded []any of strings (the form an OpReverse Context arrives in).
func builderCtxStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	switch v := m[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
