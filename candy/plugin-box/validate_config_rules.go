package box

// validate_config_rules.go — the PURE raw-config validate rules relocated out of charly core
// (K3-W2, task #13, orchestrator ruling 3(b)): validateBuildAndDistro / validateBoxBaseFrom /
// validateMergeConfig / validateBuildTunables / validateBuilderRefs operate ONLY on plain
// *spec.Config / spec.BoxConfig / *spec.DistroConfig / *spec.BuilderConfig — no CUE, no registry,
// no host-only dependency — so they move here VERBATIM, and this plugin now self-loads the raw
// config itself via the hoisted sdk/loaderkit.LoadUnifiedViaExecutor witness (the SAME canonical
// plugin-side loader candy/plugin-build/plugin-vm/plugin-fleet already share, K3-W2 unit 2) instead
// of receiving it from the host's (now-slimmed) "validate-project-checks" seam.
//
// The CUE-schema-conformance pair and the remote-candy check followed them in K-wave 2 cone R1
// unit B — see validate_schema_rules.go, which records why the two reasons this file's header used
// to give for keeping them host-side ("they need the host's spliced cross-plugin CUE schema" and
// "no executor-backed RefsCollectSeams bridge exists") were both already false when written. So the
// whole `charly box validate` rule set runs plugin-side now, and the fat "validate-project-checks"
// HostBuild seam is gone; the host answers only the registry question ("validate-word-sets").

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// loadRawProjectConfig self-loads the raw *spec.Config + *spec.DistroConfig + *spec.BuilderConfig
// for dir via the hoisted plugin-side loader witness — the same loaderkit.LoadUnifiedViaExecutor
// candy/plugin-build/plugin-vm/plugin-fleet already call, plus the SAME
// spec.ProjectDistroConfig/ProjectBuilderConfig projections charly's LoadBuildConfigForBox
// LoadBuildConfigForBox uses core-side. Returns (nil, nil, nil, nil) for a project-less directory
// (mirrors the empty-project contract every other resolve path honours) — the caller's rules then
// loop zero times, exactly like an absent host reply would.
func loadRawProjectConfig(ctx context.Context, ex *sdk.Executor, dir string) (*spec.Config, *spec.DistroConfig, *spec.BuilderConfig, error) {
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir)
	if err != nil {
		return nil, nil, nil, err
	}
	if !ok || uf == nil {
		return nil, nil, nil, nil
	}
	cfg := uf.ProjectConfig()
	distroCfg := spec.ProjectDistroConfig(uf, resolveDistroLegForValidate(ctx, ex))
	builderCfg := spec.ProjectBuilderConfig(uf)
	return cfg, distroCfg, builderCfg, nil
}

// resolveDistroLegForValidate projects one opaque distro body into a *spec.ResolvedDistro via
// InvokeProvider(kind:distro, OpResolve) — R3: the SAME small closure shape
// candy/plugin-build/resolve_legs.go's resolveDistroLeg and candy/plugin-vm's own copy already
// carry (a pure per-module callback duplicated across the module boundary, the established pattern
// for reaching a compiled-in kind provider — separate Go modules cannot share package-private
// helpers).
func resolveDistroLegForValidate(ctx context.Context, ex *sdk.Executor) func(json.RawMessage) (*spec.ResolvedDistro, error) {
	return func(body json.RawMessage) (*spec.ResolvedDistro, error) {
		params, err := json.Marshal(spec.DistroResolveInput{Distro: body})
		if err != nil {
			return nil, err
		}
		res, err := ex.InvokeProvider(ctx, "kind", "distro", sdk.OpResolve, params, nil, sdk.InvokeProviderOpts{})
		if err != nil {
			return nil, err
		}
		var reply spec.DistroResolveReply
		if len(res) > 0 {
			if err := json.Unmarshal(res, &reply); err != nil {
				return nil, fmt.Errorf("distro resolve: decode reply: %w", err)
			}
		}
		return reply.Resolved, nil
	}
}

// runRawConfigChecks runs every moved pure rule over the raw config the caller self-loaded
// (loadRawProjectConfig, hoisted into runValidateEngine so ONE load feeds both these rules and the
// CUE/remote-candy rules — R3), appending each finding into e (the SAME vErr accumulator
// runAllValidations uses). A project-less / load-failed dir contributes nothing (best-effort and
// additive, matching every other resolve path's tolerance — a load failure is already surfaced by
// the resolved-envelope diagnostics runValidateEngine merges).
func runRawConfigChecks(cfg *spec.Config, distroCfg *spec.DistroConfig, builderCfg *spec.BuilderConfig, e *vErr) {
	if cfg == nil {
		return
	}
	errs := &spec.ValidationError{}
	if distroCfg != nil {
		validateBuildAndDistro(cfg, distroCfg, errs)
	}
	validateBoxBaseFrom(cfg, spec.ResolveOpts{}, errs)
	validateMergeConfig(cfg, errs)
	validateBuildTunables(cfg, errs)
	if builderCfg != nil {
		validateBuilderRefs(cfg, builderCfg, errs)
	}
	for _, m := range errs.Errors {
		e.Add("%s", m)
	}
}

// validateBoxBaseFrom surfaces the box entity-level base⊻from mutual-exclusion as a collected
// validation error. Relocated VERBATIM from charly/validate.go (K3-W2).
func validateBoxBaseFrom(cfg *spec.Config, opts spec.ResolveOpts, errs *spec.ValidationError) {
	for name, img := range cfg.EachBox {
		if !img.IsEnabled() && !opts.ShouldIncludeDisabled(name) {
			continue
		}
		if img.HasBaseFromConflict() {
			errs.Add("box %q: from: and base: are mutually exclusive (set one; omit both for a scratch box)", name)
		}
	}
}

// validateBuildAndDistro validates build: and distro: entries. Relocated VERBATIM from
// charly/validate.go (K3-W2).
func validateBuildAndDistro(cfg *spec.Config, distroCfg *spec.DistroConfig, errs *spec.ValidationError) {
	validateBuild := func(context string, build []string) {
		for _, b := range build {
			if !distroCfg.ValidFormat(b) {
				errs.Add("%s: build entry %q is not valid (known formats: %s)", context, b, strings.Join(distroCfg.AllFormatNames(), ", "))
			}
		}
		seen := make(map[string]bool)
		for _, b := range build {
			if seen[b] {
				errs.Add("%s: duplicate build entry %q", context, b)
			}
			seen[b] = true
		}
	}

	validateBuild("defaults", cfg.Defaults.Build)
	for name, img := range cfg.EachBox {
		if !img.IsEnabled() {
			continue
		}
		validateBuild(fmt.Sprintf("box %q", name), img.Build)
	}
}

// validMergeConfigCacheModes is the allow-list for defaults.cache / image.cache (the plugin-side
// copy of charly/validate.go's validBuildCacheModes — R3-fine, a pure static map duplicated across
// the module boundary, same class as the levenshtein/findSimilarName helpers this file's siblings
// already copy verbatim).
var validMergeConfigCacheModes = map[string]bool{
	"": true, "image": true, "registry": true, "gha": true, "none": true,
}

// validateMergeConfig validates merge configuration. Relocated VERBATIM from charly/validate.go
// (K3-W2).
func validateMergeConfig(cfg *spec.Config, errs *spec.ValidationError) {
	if m := cfg.Defaults.Merge; m != nil && m.MaxMB < 0 {
		errs.Add("defaults: merge max_mb must be > 0, got %d", m.MaxMB)
	}
}

// validateBuildTunables validates the build-speed knobs on defaults: and any image entry.
// Relocated VERBATIM from charly/validate.go (K3-W2).
func validateBuildTunables(cfg *spec.Config, errs *spec.ValidationError) {
	check := func(name string, ic spec.BoxConfig) {
		if ic.Jobs != nil && *ic.Jobs < 1 {
			errs.Add("%s: jobs must be >= 1, got %d", name, *ic.Jobs)
		}
		if ic.PodmanJobs != nil && *ic.PodmanJobs < 0 {
			errs.Add("%s: podman_jobs must be >= 0, got %d", name, *ic.PodmanJobs)
		}
		if ic.PodmanJobsCap != nil && *ic.PodmanJobsCap < 1 {
			errs.Add("%s: podman_jobs_cap must be >= 1, got %d", name, *ic.PodmanJobsCap)
		}
		if !validMergeConfigCacheModes[ic.Cache] {
			errs.Add("%s: cache must be one of image|registry|gha|none, got %q", name, ic.Cache)
		}
		for i, p := range ic.ContextIgnore {
			if strings.TrimSpace(p) == "" {
				errs.Add("%s: context_ignore[%d] must not be empty", name, i)
			}
		}
		if ic.KeepImages != nil && *ic.KeepImages < 0 {
			errs.Add("%s: keep_images must be >= 0 (0 = disabled), got %d", name, *ic.KeepImages)
		}
		if ic.KeepCheckRuns != nil && *ic.KeepCheckRuns < 0 {
			errs.Add("%s: keep_check_runs must be >= 0 (0 = disabled), got %d", name, *ic.KeepCheckRuns)
		}
	}

	check("defaults", cfg.Defaults)
	for name, img := range cfg.EachBox {
		if !img.IsEnabled() {
			continue
		}
		check(fmt.Sprintf("box %q", name), img)
	}
}

// validateBuilderRefs is the HOST-NATURAL... — no longer host-natural: relocated VERBATIM from
// charly/validate.go (K3-W2), which itself named this "the host-natural half of the former
// validateBuilders" — it is pure config-navigation over cfg/builderCfg, no host coupling at all;
// the "host-natural" framing was inherited from task #60's original split, not a genuine
// M/B-clause reason. Kind-blind: builder/build TYPE words are checked against the runtime builder
// vocab, no kind switch.
func validateBuilderRefs(cfg *spec.Config, builderCfg *spec.BuilderConfig, errs *spec.ValidationError) {
	for typ, builder := range cfg.Defaults.Builder {
		if !builderCfg.ValidBuilderType(typ) {
			errs.Add("defaults.builder: build type %q is not valid (known builders: %s)", typ, strings.Join(builderCfg.BuilderNames(), ", "))
		}
		if builder != "" {
			builderImg, _, exists := cfg.ResolveBoxRef(builder)
			if !exists {
				errs.Add("defaults.builder.%s: box %q not found", typ, builder)
			} else if !builderImg.IsEnabled() {
				errs.Add("defaults.builder.%s: box %q is disabled", typ, builder)
			}
		}
	}
	for boxName, img := range cfg.EachBox {
		if !img.IsEnabled() {
			continue
		}
		for _, b := range img.Produce {
			if !builderCfg.ValidBuilderType(b) {
				errs.Add("box %q: builds entry %q is not valid (known builders: %s)", boxName, b, strings.Join(builderCfg.BuilderNames(), ", "))
			}
		}
		for typ, builder := range img.Builder {
			if !builderCfg.ValidBuilderType(typ) {
				errs.Add("box %q: builder.%s is not a valid build type (known builders: %s)", boxName, typ, strings.Join(builderCfg.BuilderNames(), ", "))
			}
			if builder == boxName {
				errs.Add("box %q: builder.%s cannot reference self", boxName, typ)
				continue
			}
			if builder != "" {
				builderImg, _, exists := cfg.ResolveBoxRef(builder)
				if !exists {
					errs.Add("box %q: builder.%s references %q which is not found", boxName, typ, builder)
					continue
				}
				if !builderImg.IsEnabled() {
					errs.Add("box %q: builder.%s references %q which is disabled", boxName, typ, builder)
					continue
				}
				if len(builderImg.Produce) > 0 && !slices.Contains(builderImg.Produce, typ) {
					errs.Add("box %q: builder.%s references %q which does not declare builds: [%s]", boxName, typ, builder, typ)
				}
			}
		}
	}
}
