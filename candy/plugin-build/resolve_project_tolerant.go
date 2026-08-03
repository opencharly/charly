package build

import (
	"context"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// resolve_project_tolerant.go — the error-TOLERANT sibling of resolve_project_word.go's
// resolveProjectEnvelope (#55 step3 unit 3-I): relocates the TOLERANT half of
// charly/validate_project_host.go's former hostBuildValidateProject onto build:project (a NEW
// sdk.OpValidate leg on the SAME "project" word, alongside 3b's sdk.OpResolve — the class:word
// bijection gate (F11) is per (class,word), not per (class,word,op), so a second Op on an
// existing word is the SAME pattern candy/plugin-build already uses for build:box + build:generate).
//
// A LoadUnified/scan failure becomes a spec.Diagnostic (skip+continue) instead of aborting —
// mirroring the DELETED host buildResolvedProjectTolerant/loadProjectForResolve's tolerant
// branch — reusing the SAME loaderkit primitives resolveProjectEnvelope already proved portable
// in 3b. What does NOT relocate here (RDD: neither proven nor attempted, per validate_project_host.go's
// own explicit, unrefuted claim): the "host-natural" RAW-config checks (the CUE-schema conformance
// trio, build/distro/merge/tunable/remote-candy validation — need the on-disk manifest bytes + the
// CUE library) and the registry-derived D-data (ProviderCapabilities/ActCapableVerbs — need
// providerRegistry.allProviders(), a genuine kernel M-mechanism per the boundary law). Those stay
// behind a SEPARATE, now-much-smaller "validate-project-checks" HostBuild leg
// (charly/validate_project_host.go) that candy/plugin-box's runValidateEngine calls IN ADDITION to
// this Op, merging both diagnostics sets + the D-data fields before running its own pure/graph rules.

// diagSeverityError mirrors charly's former diagSeverityError (validate_project_host.go) — the
// spec.Diagnostic severity for a hard tolerant-load failure.
const diagSeverityError = "error"

// resolveProjectEnvelopeTolerant is the TOLERANT project resolve: a LoadUnified or candy-scan
// failure is appended to diags and the resolve continues best-effort (empty envelope / empty
// layers) instead of aborting, so `charly box validate` can report findings on a project that
// fails to fully resolve. Mirrors resolveProjectEnvelope's steps exactly (load → vocab → scan →
// project), diverging ONLY in error handling — same RDD "byte-for-byte relocation" discipline 3b
// used, extended to the tolerant contract loadProjectForResolve/buildResolvedProjectTolerant had.
func resolveProjectEnvelopeTolerant(ctx context.Context, ex *sdk.Executor, req spec.ValidateProjectRequest) (spec.ResolvedProject, spec.Diagnostics) {
	var diags spec.Diagnostics
	addDiag := func(err error) {
		diags.Items = append(diags.Items, spec.Diagnostic{Severity: diagSeverityError, Message: err.Error()})
	}

	dir := req.Dir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}

	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir)
	if err != nil {
		// A real load failure (schema gate, parse error, …) — tolerant: diagnose, empty envelope.
		addDiag(err)
		return spec.ResolvedProject{}, diags
	}
	if !ok || uf == nil {
		// A project-less directory is NOT an error (the same empty-project contract every
		// resolved-project consumer honours) — no diagnostic, just an empty envelope.
		return spec.ResolvedProject{}, diags
	}
	cfg := uf.ProjectConfig()

	distroCfg := loaderkit.ProjectDistroConfig(uf, resolveDistroLeg(ctx, ex))
	builderCfg := loaderkit.ProjectBuilderConfig(uf)
	initCfg := loaderkit.ProjectInitConfig(uf, resolveInitLeg(ctx, ex))

	resReq := spec.ResolvedProjectRequest{Dir: dir, IncludeDisabled: req.IncludeDisabled}
	layers := map[string]spec.CandyReader{}
	localScanned, err := scanLocalLeg(ctx, ex, resReq)
	if err != nil {
		addDiag(err)
	} else {
		scanned, serr := loaderkit.ScanCandyFromLocal(localScanned, initCfg, scanSeamsLeg(ctx, ex, resReq))
		if serr != nil {
			addDiag(serr)
		} else {
			layers = scanned
		}
	}

	calver := buildkit.ComputeCalVer()
	// preResolvedBoxes=nil (a fresh per-box resolve, mirroring resolveProjectEnvelope); diags
	// non-nil makes loaderkit.ProjectResolvedProject SKIP a box whose ResolveBox fails instead of
	// aborting the whole projection (loaderkit.ProjectResolvedProject's own tolerant contract).
	rp, rerr := projectResolvedProjectLeg(ctx, ex, cfg, layers, uf, distroCfg, builderCfg, initCfg, dir, uf.Version, calver, req.IncludeDisabled, nil, &diags)
	if rerr != nil {
		// projectResolvedProjectLeg itself only returns a hard error for a non-box-resolve failure
		// (diags==nil never applies here since we always pass a non-nil diags) — tolerant: diagnose,
		// keep whatever partial rp came back (never nil per loaderkit.ProjectResolvedProject's
		// contract, but guarded below regardless).
		addDiag(rerr)
	}
	if rp == nil {
		rp = &spec.ResolvedProject{}
	}
	rp.Primaries = loaderkit.LoaderThreadedViaExecutor(ctx, ex).Primaries
	return *rp, diags
}
