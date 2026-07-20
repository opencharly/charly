package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"
)

// validate_project_host.go — the HOST side of the `charly box validate` engine relocation (task #60,
// Unit B). The validate ENGINE moves to the compiled-in candy/plugin-box, which runs the pure
// per-kind/op rules over the resolved-project envelope + re-runs the resolution-graph checks via
// sdk/deploykit. The HOST keeps ONLY what a plugin structurally CANNOT do:
//
//   1. the error-TOLERANT resolved-project projection (validate MUST run on a broken project — a box
//      that fails to resolve becomes a spec.Diagnostic, not a fatal abort), and
//   2. the host-natural checks that need the RAW authored config a projection does not carry: the
//      CUE-schema conformance trio (manifest bytes + the cue library) + the build-tunable / merge
//      rules (defaults + per-box tunables dropped from the envelope) + the box base⊻from XOR (raw
//      pre-resolve base/from, which the tolerant-skipped envelope cannot carry).
//
// Both ride back to the plugin over ONE `validate-project` HostBuild seam (#46) as a
// spec.ValidateProjectReply{Project (tolerant partial), Diagnostics}. The plugin merges these host
// findings with its own pure-rule + graph findings for the verdict.

// validateProjectBuilderKind is the F11 hostBuilders key — a generic action noun, never a provider word.
const validateProjectBuilderKind = "validate-project"

// diagSeverityError is the spec.Diagnostic severity for a hard validation error (empty defaults to
// error per the wire contract, but the plugin's HasErrors() classifies non-"warning" as error, so we
// stamp it explicitly). Shared by projectResolvedProject's tolerant ResolveBox branch.
const diagSeverityError = "error"

// loadedProject bundles the raw loaded pieces the two project builders (fail-fast + tolerant) share:
// the config, the scanned candies, the discovered unified file, the build vocabulary, and the schema
// version. empty marks a project-less directory (ErrNoCharlyYml → the empty-project contract).
type loadedProject struct {
	cfg        *Config
	layers     map[string]*Candy
	uf         *UnifiedFile // nil when absent or its load/discover errored
	distroCfg  *buildkit.DistroConfig
	builderCfg *buildkit.BuilderConfig
	initCfg    *InitConfig
	version    string
	empty      bool
}

// loadProjectForResolve is the ONE load path both buildResolvedProjectFromDir (fail-fast) and
// buildResolvedProjectTolerant (validate) drive (R3). When diags is nil it is FAIL-FAST: any
// LoadConfig/Scan/LoadUnified/ApplyDiscover error aborts with that error. When diags is non-nil it is
// ERROR-TOLERANT: each such error becomes a spec.Diagnostic and the load continues best-effort (no
// config → empty; a scan failure → zero candies; a unified-load failure → no deploy/template fill), so
// validate runs on a broken project. The build vocabulary is registered (so ResolveBox resolves
// distro/builder) exactly as before.
func loadProjectForResolve(dir string, opts ResolveOpts, diags *spec.Diagnostics) (*loadedProject, error) {
	lp := &loadedProject{layers: map[string]*Candy{}}

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
		if derr := uf.ApplyDiscover(dir); derr != nil {
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

// buildResolvedProjectTolerant is the error-TOLERANT sibling of buildResolvedProjectFromDir: load and
// resolve failures become spec.Diagnostic entries (skip+continue) instead of aborting. Returns the
// PARTIAL envelope, the loaded raw pieces (which the host-natural checks read), and the resolve
// diagnostics gathered so far. Used by the validate-project host-builder.
func buildResolvedProjectTolerant(dir string, opts ResolveOpts) (*spec.ResolvedProject, *loadedProject, spec.Diagnostics) {
	diags := &spec.Diagnostics{}
	lp, _ := loadProjectForResolve(dir, opts, diags) // tolerant: the error return is always nil
	if lp.empty {
		return &spec.ResolvedProject{}, lp, *diags
	}
	// projectResolvedProject with a non-nil diags never returns a Go error (its only error path is the
	// fail-fast diags==nil branch), so the partial envelope is always usable.
	rp, _ := projectResolvedProject(lp.cfg, lp.layers, lp.uf, lp.distroCfg, lp.builderCfg, lp.initCfg, dir, lp.version, opts, diags)
	return rp, lp, *diags
}

// runHostNaturalValidateChecks runs the validation rules that a plugin structurally CANNOT (they read
// RAW authored config a projection does not carry) over the loaded project, appending each finding as
// an error-severity spec.Diagnostic. This is the ONLY validation left host-side after the engine moves
// to plugin-box:
//   - the CUE-schema conformance trio (validateCandyCUESchemas / validateProjectCUESchemas — which
//     folds validateVocabularyCollections — needs the on-disk manifest bytes + the cue library);
//   - validateBuildAndDistro (the authored `build:` list on defaults + each box against the DYNAMIC
//     distro-format vocab — both raw, neither on the envelope);
//   - validateBoxBaseFrom (the base⊻from XOR reads raw pre-resolve cfg.eachBox; a base+from box fails
//     ResolveBox and is tolerant-skipped from the envelope, so only the raw config catches it);
//   - validateMergeConfig / validateBuildTunables (defaults.merge + per-box jobs/cache/keep_* tunables
//     are dropped from ResolvedBoxView / have no Defaults on the envelope);
//   - validateRemoteCandies (the CollectRemoteRefs version-conflict resolver reads raw (repo,version)
//     ref data the bare-form CandyView.Require cannot carry).
//
// Every function is KIND-BLIND with ONE tracked exception the orchestrator reviews at tree-final: the
// hardcoded collection-kind WORD LIST inside validateProjectCUESchemas (a legacy root-shape arm; task
// #60 CONDITION 1 — restructure to cueKindDefs D-data or delete the dead legacy path per the ruling).
func runHostNaturalValidateChecks(lp *loadedProject, dir string, opts ResolveOpts, diags *spec.Diagnostics) {
	if lp == nil || lp.cfg == nil {
		return
	}
	errs := &ValidationError{}
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

// hostBuildValidateProject is the "validate-project" host-builder (#46): load the project at req.Dir
// (empty = cwd) TOLERANTLY, project the partial envelope, run the host-natural checks, and return the
// combined spec.ValidateProjectReply{Project, Diagnostics}. The plugin merges these host diagnostics
// with its own pure-rule + resolution-graph findings for the final verdict + exit code.
func hostBuildValidateProject(_ context.Context, req spec.ValidateProjectRequest, _ buildEngineContext) (spec.ValidateProjectReply, error) {
	dir := req.Dir
	if dir == "" {
		d, err := os.Getwd()
		if err != nil {
			return spec.ValidateProjectReply{}, err
		}
		dir = d
	}
	opts := ResolveOpts{IncludeDisabled: req.IncludeDisabled}
	rp, lp, diags := buildResolvedProjectTolerant(dir, opts)
	runHostNaturalValidateChecks(lp, dir, opts, &diags)
	fillValidateWordSets(rp, lp)
	return spec.ValidateProjectReply{Project: rp, Diagnostics: diags}, nil
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
			scanPlan(layer.plan)
		}
	}
	if lp.cfg != nil {
		for _, img := range lp.cfg.eachBox {
			scanPlan(img.Plan)
		}
	}
}

var _ = func() bool {
	registerHostBuilder(validateProjectBuilderKind, typedHostBuilder(validateProjectBuilderKind, hostBuildValidateProject))
	return true
}()

// validateProjectForBuild is the pre-build validation GATE (task #60, (C-refined)): the validate ENGINE
// lives in candy/plugin-box, so `charly box build`/`generate` (NewGenerator) no longer calls Validate()
// directly — it dispatches to the compiled-in validate capability BY WORD with a structured OpValidate
// op (the SAME registry-dispatch shape the build path already uses for OpEmit/OpResolve) over an in-proc
// reverse channel, and consumes the returned spec.Diagnostics as a HARD gate (the error text mirrors the
// former ValidationError.Error() for parity). Kind-blind M (registry-by-word). Named exit K3 — when the
// build engine itself becomes plugin-build, this call becomes a plugin↔plugin InvokeProvider.
func validateProjectForBuild(dir string, opts ResolveOpts) error {
	prov, ok := providerRegistry.resolve(ClassCommand, "validate")
	if !ok {
		return fmt.Errorf("pre-build validation: the validate capability (command:validate) is not compiled in")
	}
	reqJSON, err := json.Marshal(spec.ValidateProjectRequest{Dir: dir, IncludeDisabled: opts.IncludeDisabled})
	if err != nil {
		return err
	}
	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	res, err := prov.Invoke(ctx, &Operation{Reserved: "validate", Op: sdk.OpValidate, Params: reqJSON})
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
