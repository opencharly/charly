package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// validate_project_host.go — the HOST side of the `charly box validate` engine relocation (task #60,
// Unit B). The validate ENGINE moves to the compiled-in candy/plugin-box, which runs the pure
// per-kind/op rules over the resolved-project envelope + re-runs the resolution-graph checks via
// sdk/deploykit.
//
// #55 step3 unit 3-I relocated reason 1 below (the error-TOLERANT resolved-project projection)
// onto build:project's ops.OpValidate leg (candy/plugin-build/resolve_project_tolerant.go) — proven
// portable by 3b's SAME-shaped fail-fast relocation. What STAYS host is reason 2, which a plugin
// structurally still cannot do:
//
//   2. the host-natural checks that need the RAW authored config a projection does not carry: the
//      CUE-schema conformance trio (manifest bytes + the cue library) + the build-tunable / merge
//      rules (defaults + per-box tunables dropped from the envelope) + the box base⊻from XOR (raw
//      pre-resolve base/from, which the tolerant-skipped envelope cannot carry) + the
//      registry-derived D-data (ProviderCapabilities/ActCapableVerbs — providerRegistry is a
//      genuine kernel M-mechanism per the boundary law).
//
// This now rides back over the SLIMMED `validate-project-checks` HostBuild seam (renamed from
// `validate-project`, #46) as a spec.ValidateProjectReply{Project (D-data fields ONLY),
// Diagnostics}. candy/plugin-box's runValidateEngine calls BOTH build:project(ops.OpValidate) (its own
// tolerant envelope + resolve-diagnostics) AND this leg (host-natural diagnostics + D-data),
// merges them onto one envelope, then runs its own pure-rule + graph findings for the verdict.

// validateProjectChecksBuilderKind is the F11 hostBuilders key — a generic action noun, never a
// provider word. Renamed from "validate-project" (#55 step3 unit 3-I) now that this leg serves
// ONLY the host-natural-checks half; the tolerant envelope projection moved to build:project.
const validateProjectChecksBuilderKind = "validate-project-checks"

// diagSeverityError is the spec.Diagnostic severity for a hard validation error (empty defaults to
// error per the wire contract, but the plugin's HasErrors() classifies non-"warning" as error, so we
// stamp it explicitly). Shared by loadProjectForResolve's tolerant load-error branch and
// runHostNaturalValidateChecks.
const diagSeverityError = "error"

// loadedProject bundles the raw loaded pieces the two project builders (fail-fast + tolerant) share:
// the config, the scanned candies, the discovered unified file, the build vocabulary, and the schema
// version. empty marks a project-less directory (ErrNoCharlyYml → the empty-project contract).
type loadedProject struct {
	cfg        *spec.Config
	layers     map[string]spec.CandyReader
	uf         *spec.UnifiedFile // nil when absent or its load/discover errored
	distroCfg  *spec.DistroConfig
	builderCfg *spec.BuilderConfig
	initCfg    *spec.InitConfig
	version    string
	empty      bool
}

// loadProjectForResolve is the ONE load path both hostBuildNamespaced's fail-fast namespaced-box
// resolve (host_build_buildengine.go, diags==nil) and
// hostBuildValidateProjectChecks (diags!=nil, TOLERANT — feeding the host-natural checks below,
// #55 step3 unit 3-I) drive (R3). When diags is nil it is FAIL-FAST: any
// LoadConfig/Scan/LoadUnified/ApplyDiscover error aborts with that error. When diags is non-nil it is
// ERROR-TOLERANT: each such error becomes a spec.Diagnostic and the load continues best-effort (no
// config → empty; a scan failure → zero candies; a unified-load failure → no deploy/template fill), so
// the host-natural checks still run on a broken project. The build vocabulary is registered (so
// ResolveBox resolves distro/builder) exactly as before.
func loadProjectForResolve(dir string, opts spec.ResolveOpts, diags *spec.Diagnostics) (*loadedProject, error) {
	lp := &loadedProject{layers: map[string]spec.CandyReader{}}

	cfg, err := LoadConfig(dir)
	if errors.Is(err, ErrNoCharlyYml) {
		// A project-less directory resolves to an EMPTY project, not an error — the same contract
		// `charly box list boxes` has always honoured (the charly-mcp box.list.boxes tool runs in
		// CHARLY_PROJECT_DIR before any charly.yml exists, so it must exit 0 empty).
		lp.empty = true
		return lp, nil
	}
	if err != nil {
		if diags == nil {
			return nil, err
		}
		addLoadDiag(diags, err)
		lp.empty = true // no config to project or check
		return lp, nil
	}
	lp.cfg = cfg

	// The build vocabulary is BOTH registered (so ResolveBox resolves distro/builder) AND projected
	// into the envelope's distro/builder/init members (the validate consumer).
	lp.distroCfg, lp.builderCfg, lp.initCfg, _ = LoadDefaultBuildConfig(dir)
	if lp.distroCfg != nil {
		RegisterBuildVocabulary(lp.distroCfg)
	}

	// InitCfg threads the init-system host-completion pass into the scan pipeline —
	// mirrors the SAME opts.InitCfg = defaultInitCfg pattern the deleted NewGenerator
	// used (#55 step3 3-II; the real build/generate drive threads it plugin-side now,
	// candy/plugin-build/resolve.go). A
	// spec.CandyReader is read-only once wrapped, so InitSystems must be populated
	// before ScanAllCandyWithConfigOpts wraps each candy. Required since #67: this
	// scan's output (lp.layers) feeds rp.Candies/rp.CandyModels — the wire envelope
	// plugin-build's Generator consumes for its own per-candy HasInit() lookups
	// during Containerfile emission (EmitInitFragmentStages).
	opts.InitCfg = lp.initCfg

	layers, err := ScanAllCandyWithConfigOpts(dir, cfg, opts)
	if err != nil {
		if diags == nil {
			return nil, err
		}
		addLoadDiag(diags, err)
	} else {
		lp.layers = layers
	}

	uf, present, uerr := LoadUnified(dir)
	if uerr != nil {
		if diags == nil {
			return nil, uerr
		}
		addLoadDiag(diags, uerr)
	} else if present {
		if derr := ApplyDiscover(uf, dir); derr != nil {
			if diags == nil {
				return nil, derr
			}
			addLoadDiag(diags, derr)
		} else {
			lp.uf = uf
			lp.version = uf.Version
		}
	}
	return lp, nil
}

// addLoadDiag appends a load/resolve failure as an error-severity spec.Diagnostic. The Message is the
// raw error string (no extra wrapping) so a validate verdict on a project that fails to load surfaces
// the SAME text the former fail-fast `charly box validate` printed (behavior parity).
func addLoadDiag(diags *spec.Diagnostics, err error) {
	diags.Items = append(diags.Items, spec.Diagnostic{Severity: diagSeverityError, Message: err.Error()})
}

// runHostNaturalValidateChecks runs the validation rules that a plugin structurally CANNOT (they read
// RAW authored config a projection does not carry) over the loaded project, appending each finding as
// an error-severity spec.Diagnostic. This is the ONLY validation left host-side after the engine moves
// to plugin-box:
//   - the CUE-schema conformance pair (validateCandyCUESchemas / validateProjectCUESchemas — needs
//     the on-disk manifest bytes + the cue library; the modern per-kind LOAD-time plugin gate now
//     covers what the former validateVocabularyCollections did for non-box kinds — dead-code-radical-
//     removal-batch deletion, c9befd83 already cut its production call site);
//   - validateBuildAndDistro (the authored `build:` list on defaults + each box against the DYNAMIC
//     distro-format vocab — both raw, neither on the envelope);
//   - validateBoxBaseFrom (the base⊻from XOR reads raw pre-resolve cfg.EachBox; a base+from box fails
//     ResolveBox and is tolerant-skipped from the envelope, so only the raw config catches it);
//   - validateMergeConfig / validateBuildTunables (defaults.merge + per-box jobs/cache/keep_* tunables
//     are dropped from ResolvedBoxView / have no Defaults on the envelope);
//   - validateRemoteCandies (the CollectRemoteRefs version-conflict resolver reads raw (repo,version)
//     ref data the bare-form CandyView.Require cannot carry).
//
// Every function is KIND-BLIND with ONE tracked exception the orchestrator reviews at tree-final: the
// hardcoded collection-kind WORD LIST inside validateProjectCUESchemas (a legacy root-shape arm; task
// #60 CONDITION 1 — restructure to cueKindDefs D-data or delete the dead legacy path per the ruling).
func runHostNaturalValidateChecks(lp *loadedProject, dir string, opts spec.ResolveOpts, diags *spec.Diagnostics) {
	if lp == nil || lp.cfg == nil {
		return
	}
	errs := &spec.ValidationError{}
	if lp.distroCfg != nil {
		validateBuildAndDistro(lp.cfg, lp.distroCfg, errs)
	}
	validateCandyCUESchemas(lp.layers, errs)
	validateProjectCUESchemas(lp.cfg, dir, opts, errs)
	validateBoxBaseFrom(lp.cfg, opts, errs)
	validateMergeConfig(lp.cfg, errs)
	validateBuildTunables(lp.cfg, errs)
	if lp.builderCfg != nil {
		validateBuilderRefs(lp.cfg, lp.builderCfg, errs)
	}
	validateRemoteCandies(lp.cfg, lp.layers, errs)
	for _, e := range errs.Errors {
		diags.Items = append(diags.Items, spec.Diagnostic{Severity: diagSeverityError, Message: e})
	}
}

// hostBuildValidateProjectChecks is the "validate-project-checks" host-builder (#55 step3 unit
// 3-I) — the SLIMMED remainder of the former "validate-project" seam (#46) after the TOLERANT
// resolved-project projection relocated onto build:project's ops.OpValidate leg
// (candy/plugin-build/resolve_project_tolerant.go, which reuses 3b's proven-portable loaderkit
// primitives instead of this host's LoadConfig/ScanAllCandyWithConfigOpts). This leg's OWN
// tolerant load (loadProjectForResolve, kept — also still used by build_overlay.go's fail-fast
// call) exists ONLY to feed the host-natural checks + the registry D-data below with the RAW
// *Config/*spec.DistroConfig/*BuilderConfig a projected envelope does not carry — it no
// longer projects an envelope itself (projectResolvedProject/buildResolvedProjectTolerant,
// DELETED). Returns a spec.ValidateProjectReply whose Project carries ONLY
// ProviderCapabilities/ActCapableVerbs; candy/plugin-box's runValidateEngine calls THIS leg
// alongside build:project(ops.OpValidate) and merges both replies' Diagnostics + this reply's D-data
// onto the plugin's own tolerant envelope before running its pure/graph rules.
func hostBuildValidateProjectChecks(_ context.Context, req spec.ValidateProjectRequest, _ buildEngineContext) (spec.ValidateProjectReply, error) {
	dir := req.Dir
	if dir == "" {
		d, err := os.Getwd()
		if err != nil {
			return spec.ValidateProjectReply{}, err
		}
		dir = d
	}
	opts := spec.ResolveOpts{IncludeDisabled: req.IncludeDisabled}
	// loadDiags is DISCARDED (not returned): a LoadConfig/scan/LoadUnified failure here is the SAME
	// underlying disk-file failure candy/plugin-build's build:project(ops.OpValidate) leg's OWN tolerant
	// load already reports in envReply.Diagnostics (both legs load the identical charly.yml) —
	// surfacing it from BOTH legs would duplicate the finding verbatim in the merged verdict (caught
	// live: a base⊻from CUE-disjunction load failure printed twice before this fix). Only
	// runHostNaturalValidateChecks's OWN rule findings (checksDiags) are genuinely NEW information
	// the plugin's envelope resolve cannot produce.
	loadDiags := &spec.Diagnostics{}
	lp, _ := loadProjectForResolve(dir, opts, loadDiags) // tolerant: the error return is always nil
	checksDiags := &spec.Diagnostics{}
	runHostNaturalValidateChecks(lp, dir, opts, checksDiags)
	rp := &spec.ResolvedProject{}
	fillValidateWordSets(rp, lp)
	return spec.ValidateProjectReply{Project: rp, Diagnostics: *checksDiags}, nil
}

// fillValidateWordSets projects the two REGISTRY-derived D-data word sets the validate plugin consumes
// so it never dials the host registry (task #60 ruling): ProviderCapabilities (every compiled-in
// provider as "<class>:<word>" — validatePluginCandy checks a `source: builtin` candy's declared
// providers against it) and ActCapableVerbs (the plugin WORDS whose act form has a build/deploy install
// path — validateCheck's act-form rule). ActCapableVerbs is computed by running the SAME host
// opActsInBuildDeploy the core validator used over every distinct plugin word in the project's plan Ops,
// so builtin ProvisionActor/TypedStep/BuildEmitter rejection is PRESERVED byte-for-byte.
func fillValidateWordSets(rp *spec.ResolvedProject, lp *loadedProject) {
	if rp == nil || lp == nil {
		return
	}
	// Register the external verbs the scanned candies declare (Validate() did this too) so an
	// unconnected external verb resolves as act-capable / a valid provider before we enumerate.
	registerExternalVerbsFromCandies(lp.layers)

	for _, p := range providerRegistry.allProviders() {
		rp.ProviderCapabilities = append(rp.ProviderCapabilities, string(p.Class())+":"+p.Reserved())
	}

	seen := map[string]bool{}
	addWord := func(w string) {
		if w == "" || seen[w] {
			return
		}
		seen[w] = true
		if opActsInBuildDeploy(&spec.Op{Plugin: w}) {
			rp.ActCapableVerbs = append(rp.ActCapableVerbs, w)
		}
	}
	scanPlan := func(plan []spec.Step) {
		for i := range plan {
			op := &plan[i].Op
			if len(op.VerbsSet()) == 0 {
				continue
			}
			if verb, err := op.Kind(); err == nil && verb == "plugin" {
				addWord(op.Plugin)
			}
		}
	}
	for _, layer := range lp.layers {
		if layer != nil {
			scanPlan(layer.PlanSteps())
		}
	}
	if lp.cfg != nil {
		for _, img := range lp.cfg.EachBox {
			scanPlan(img.Plan)
		}
	}
}

var _ = func() bool {
	registerHostBuilder(validateProjectChecksBuilderKind, typedHostBuilder(validateProjectChecksBuilderKind, hostBuildValidateProjectChecks))
	return true
}()

// validateProjectForBuild is the pre-build validation GATE (task #60, (C-refined)): the validate ENGINE
// lives in candy/plugin-box, so `charly box build`/`generate` no longer calls Validate() directly —
// it dispatches to the compiled-in validate capability BY WORD with a structured ops.OpValidate op (the
// SAME registry-dispatch shape the build path already uses for ops.OpEmit/ops.OpResolve) over an in-proc
// reverse channel, and consumes the returned spec.Diagnostics as a HARD gate (the error text mirrors the
// former spec.ValidationError.Error() for parity). Kind-blind M (registry-by-word). #55 step3 3-II
// deleted this function's last production caller, the former host-side NewGenerator — the real
// build/generate drive's own pre-build validate now runs plugin-side (candy/plugin-build/resolve.go
// step 5, validateProjectLeg, a genuine plugin↔plugin InvokeProvider — the K3 named exit this comment
// used to describe as future work is DONE). This function survives as test-only coverage (see its
// callers in validate_fixture_test.go / plugin_installstep_envelope_parity_test.go's
// testFullResolveGenerator) reproducing the SAME validate dispatch for fixture-driven unit tests.
func validateProjectForBuild(dir string, opts spec.ResolveOpts) error {
	prov, ok := providerRegistry.resolve(ClassCommand, "validate")
	if !ok {
		return fmt.Errorf("pre-build validation: the validate capability (command:validate) is not compiled in")
	}
	reqJSON, err := json.Marshal(spec.ValidateProjectRequest{Dir: dir, IncludeDisabled: opts.IncludeDisabled})
	if err != nil {
		return err
	}
	ctx := specexec.ContextWithExecutor(context.Background(),
		specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	res, err := prov.Invoke(ctx, &Operation{Reserved: "validate", Op: ops.OpValidate, Params: reqJSON})
	if err != nil {
		return err
	}
	var diags spec.Diagnostics
	if res != nil && len(res.JSON) > 0 {
		if uerr := json.Unmarshal(res.JSON, &diags); uerr != nil {
			return fmt.Errorf("pre-build validation: decode diagnostics: %w", uerr)
		}
	}
	msgs := make([]string, 0, len(diags.Items))
	for _, it := range diags.Items {
		if it.Severity == "warning" {
			continue
		}
		msgs = append(msgs, it.Message)
	}
	if len(msgs) == 0 {
		return nil
	}
	if len(msgs) == 1 {
		return fmt.Errorf("validation error: %s", msgs[0])
	}
	return fmt.Errorf("%d validation errors:\n\n  %s", len(msgs), strings.Join(msgs, "\n  "))
}
