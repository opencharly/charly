package build

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// resolve_project_word.go — the `build:project` word (#55 step3 unit 3b): the PLUGIN-SIDE
// replacement for charly core's now-deleted resolved-project host seam
// (charly/resolved_project_host.go's hostBuildResolvedProject/buildResolvedProjectFromDir). The
// ~8 external consumers (candy/plugin-box, plugin-bundle ×2, plugin-check, plugin-preempt,
// plugin-installstep, plugin-status, plugin-substrate) that used to reach that now-deleted host
// seam directly now call
// `exec.InvokeProvider(ctx, "build", "project", sdk.OpResolve, reqJSON, nil, ...)` instead — a
// class-generic word on the EXISTING `build` provider (candy/plugin-build already owns this exact
// resolve, box/generate, ensure, pkg), reached over the EXISTING plugin↔plugin InvokeProvider
// peer-dispatch. No new seam invented (F11).
//
// BYTE-FOR-BYTE RELOCATION (cutover-policy "relocation step-sequence PARITY"): this deliberately
// mirrors the DELETED host projector's exact steps — load, vocab, scan, resolve boxes, project the
// envelope — and DELIBERATELY OMITS render-prep (dg.RenderPrepAll), unlike resolveBuildEngine's
// (this package's build:box/build:generate resolve) full pipeline. The current host projector
// never render-prepped ROOT boxes either (only namespaced ones — formerly via the deleted
// host namespaced-box fill's own tempGen.toDeploykit().RenderPrepBox, now via the plugin-side
// namespace walk's deploykit.FillNamespaceBoxViews (resolve_legs.go) — an existing,
// UNTOUCHED asymmetry tracked separately as task #69). Changing that asymmetry here would smuggle
// a behavior change into a pure boundary move — forbidden by the parity requirement.
func resolveProjectEnvelope(ctx context.Context, ex *sdk.Executor, req spec.ResolvedProjectRequest) (spec.ResolvedProject, error) {
	dir := req.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return spec.ResolvedProject{}, err
		}
		dir = cwd
	}

	// LocalSuperproject mirrors the deleted charly/resolved_project_host.go's
	// applySelfSuperprojectOverride(dir) call — reproduced here PURELY (os/exec + loaderkit, zero
	// host-only coupling), since a compiled-in plugin shares the host's OS process/environment, so
	// os.Setenv here is visible to the SAME per-call ensureRepoLeg
	// EnsureRepoDownloaded call that reads CHARLY_REPO_OVERRIDE. Used today by
	// candy/plugin-check/checkproject.go.
	if req.LocalSuperproject {
		restore := applySelfSuperprojectOverridePlugin(dir)
		defer restore()
	}

	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir)
	if err != nil {
		return spec.ResolvedProject{}, err
	}
	if !ok || uf == nil {
		// Project-less directory → empty envelope (the SAME empty-project contract the deleted
		// buildResolvedProjectFromDir/loadProjectForResolve honoured).
		return spec.ResolvedProject{}, nil
	}
	cfg := uf.ProjectConfig()

	distroCfg := loaderkit.ProjectDistroConfig(uf, resolveDistroLeg(ctx, ex))
	builderCfg := loaderkit.ProjectBuilderConfig(uf)
	initCfg := loaderkit.ProjectInitConfig(uf, resolveInitLeg(ctx, ex))

	// SCAN: threads req VERBATIM (Dir/IncludeDisabled/ExtraCandyRefs) — unlike resolveBuildEngine's
	// own scan (which hardcodes ExtraCandyRefs: nil, since build/generate never widens the scan),
	// the deleted host projector DID thread ExtraCandyRefs through ScanAllCandyWithConfigOpts, so
	// this must too (candy/plugin-installstep + plugin-bundle both rely on it for add_candy: refs).
	localScanned, err := scanLocalLeg(ctx, ex, uf, dir, distroCfg)
	if err != nil {
		return spec.ResolvedProject{}, err
	}
	layers, err := loaderkit.ScanCandyFromLocal(localScanned, initCfg, scanSeamsLeg(ctx, ex, req, cfg, distroCfg))
	if err != nil {
		return spec.ResolvedProject{}, err
	}

	// No build-time plugin CONNECT, no pre-build VALIDATE gate — the deleted host projector never
	// ran either (those are resolveBuildEngine-only, for the actual build/generate drive).

	calver := buildkit.ComputeCalVer()

	// preResolvedBoxes=nil: a FRESH per-box ResolveBox loop, no render-prep — byte-identical to the
	// deleted host projector's own fresh resolve (never fed pre-rendered boxes).
	rp, err := projectResolvedProjectLeg(ctx, ex, cfg, layers, uf, distroCfg, builderCfg, initCfg, dir, uf.Version, calver, req.IncludeDisabled, nil, nil)
	if err != nil {
		return spec.ResolvedProject{}, err
	}

	// Primaries: the SAME registry-derived D-fact snapshot the deleted host projector filled via
	// loaderThreaded().Primaries — reached here through the ALREADY-EXISTING "loader-threaded"
	// HostBuild leg via loaderkit.LoaderThreadedViaExecutor (no new seam).
	rp.Primaries = loaderkit.LoaderThreadedViaExecutor(ctx, ex).Primaries

	return *rp, nil
}

// applySelfSuperprojectOverridePlugin is the plugin-side, pure reproduction of charly core's
// (deleted-site) applySelfSuperprojectOverride/selfSuperprojectOverridePair (charly/refs.go): it
// makes this resolve obey the same local-candy rule as a subsequent bed session by pointing the
// remote-ref resolver at the checkout's OWN superproject working tree, when this project is
// checked out as a git submodule. Zero host-only dependency (os/exec + loaderkit.RootRepoIdentity),
// so it is reproduced here rather than round-tripped through a host leg.
func applySelfSuperprojectOverridePlugin(projectDir string) func() {
	pair := selfSuperprojectOverridePairPlugin(projectDir)
	if pair == "" {
		return func() {}
	}
	old, had := os.LookupEnv(repoOverrideEnv)
	_ = os.Setenv(repoOverrideEnv, mergeRepoOverridesPlugin(old, pair))
	return func() {
		if had {
			_ = os.Setenv(repoOverrideEnv, old)
			return
		}
		_ = os.Unsetenv(repoOverrideEnv)
	}
}

// repoOverrideEnv mirrors charly core's RepoOverrideEnv (charly/refs.go) — a plain literal, no
// host-only logic behind it.
const repoOverrideEnv = "CHARLY_REPO_OVERRIDE"

func selfSuperprojectOverridePairPlugin(projectDir string) string {
	out, err := exec.Command("git", "-C", projectDir, "rev-parse", "--show-superproject-working-tree").Output()
	if err != nil {
		return ""
	}
	superDir := strings.TrimSpace(string(out))
	if superDir == "" {
		return ""
	}
	identity := loaderkit.RootRepoIdentity(superDir)
	if identity == "" {
		return ""
	}
	return identity + "=" + superDir
}

func mergeRepoOverridesPlugin(existing, add string) string {
	existing = strings.TrimSpace(existing)
	add = strings.TrimSpace(add)
	switch {
	case existing == "":
		return add
	case add == "":
		return existing
	default:
		return existing + "," + add
	}
}
