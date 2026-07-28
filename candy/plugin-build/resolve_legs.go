package build

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// resolve_legs.go — the plugin-side leg helpers the build-engine RESOLVE (resolve.go, U6) reaches the
// host for. Each is a thin HostBuild / InvokeProvider wrapper over a charly `buildengine-*` host leg
// (charly/host_build_buildengine.go) or a compiled-in peer plugin. The pattern mirrors the K1 loader
// witness legs (candy/plugin-bundle) — only the genuinely host-coupled steps cross the wire.

// hostBuildJSON marshals req, dispatches HostBuild(kind), and decodes the reply into *Reply. A void
// leg passes a nil *Reply (out ignored).
func hostBuildJSON[Req any](ctx context.Context, ex *sdk.Executor, kind string, req Req, out any) error {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("%s: marshal request: %w", kind, err)
	}
	replyJSON, err := ex.HostBuild(ctx, kind, reqJSON)
	if err != nil {
		return fmt.Errorf("%s: %w", kind, err)
	}
	if out != nil && len(replyJSON) > 0 {
		if err := json.Unmarshal(replyJSON, out); err != nil {
			return fmt.Errorf("%s: decode reply: %w", kind, err)
		}
	}
	return nil
}

// hostVoidLeg dispatches a HostBuild leg that returns no data (the reply is empty/error-only).
func hostVoidLeg[Req any](ctx context.Context, ex *sdk.Executor, kind string, req Req) error {
	return hostBuildJSON(ctx, ex, kind, req, nil)
}

// --- vocab resolve callbacks (plugin↔plugin InvokeProvider over the kind:distro/init OpResolve legs) ---

func resolveDistroLeg(ctx context.Context, ex *sdk.Executor) func(json.RawMessage) (*spec.ResolvedDistro, error) {
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

func resolveInitLeg(ctx context.Context, ex *sdk.Executor) func(json.RawMessage) (*spec.ResolvedInit, error) {
	return func(body json.RawMessage) (*spec.ResolvedInit, error) {
		params, err := json.Marshal(spec.InitResolveRequest{Config: &spec.InitResolveInput{Init: body}})
		if err != nil {
			return nil, err
		}
		res, err := ex.InvokeProvider(ctx, "kind", "init", sdk.OpResolve, params, nil, sdk.InvokeProviderOpts{})
		if err != nil {
			return nil, err
		}
		var reply spec.InitResolveReply
		if len(res) > 0 {
			if err := json.Unmarshal(res, &reply); err != nil {
				return nil, fmt.Errorf("init resolve config: decode reply: %w", err)
			}
		}
		return reply.Resolved, nil
	}
}

func resolveResourceLeg(ctx context.Context, ex *sdk.Executor) func(json.RawMessage) (*spec.ResolvedResource, error) {
	return func(body json.RawMessage) (*spec.ResolvedResource, error) {
		params, err := json.Marshal(spec.ResourceResolveInput{Resource: body})
		if err != nil {
			return nil, err
		}
		res, err := ex.InvokeProvider(ctx, "kind", "resource", sdk.OpResolve, params, nil, sdk.InvokeProviderOpts{})
		if err != nil {
			return nil, err
		}
		var reply spec.ResourceResolveReply
		if len(res) > 0 {
			if err := json.Unmarshal(res, &reply); err != nil {
				return nil, fmt.Errorf("resource resolve: decode reply: %w", err)
			}
		}
		return reply.Resolved, nil
	}
}

// --- scan legs ---

// scanLocalLeg runs the bootstrap-delicate local candy scan (parseCandyYAML→buildCandy) host-side and
// returns the unfinalized ScannedCandy map (the plugin runs the finalize + remote fetch fixpoint).
func scanLocalLeg(ctx context.Context, ex *sdk.Executor, rr spec.ResolvedProjectRequest) (map[string]spec.ScannedCandy, error) {
	var out map[string]spec.ScannedCandy
	if err := hostBuildJSON(ctx, ex, "buildengine-scan-local", rr, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// scanSeamsLeg wires the three host-coupled ScanSeams legs (the reachability-scoped remote-ref walk,
// the git clone/cache, the per-candy remote manifest scan) the plugin's loaderkit.ScanCandyFromLocal
// fetch fixpoint reaches for. localScanned reloads host-side (deterministic; identical to the plugin's).
func scanSeamsLeg(ctx context.Context, ex *sdk.Executor, rr spec.ResolvedProjectRequest) loaderkit.ScanSeams {
	return loaderkit.ScanSeams{
		CollectRemoteRefs: func(_ map[string]spec.ScannedCandy) ([]loaderkit.RemoteDownload, error) {
			var out []loaderkit.RemoteDownload
			if err := hostBuildJSON(ctx, ex, "buildengine-collect-remote-refs", rr, &out); err != nil {
				return nil, err
			}
			return out, nil
		},
		EnsureRepo: func(repoPath, version string) (string, error) {
			var out struct {
				Dir string `json:"dir"`
			}
			if err := hostBuildJSON(ctx, ex, "buildengine-ensure-repo", map[string]string{"repo": repoPath, "version": version}, &out); err != nil {
				return "", err
			}
			return out.Dir, nil
		},
		ScanRemote: func(cacheDir, repoPath string, wantRefs map[string]bool) (map[string]spec.ScannedCandy, error) {
			refs := make([]string, 0, len(wantRefs))
			for r := range wantRefs {
				refs = append(refs, r)
			}
			var out map[string]spec.ScannedCandy
			if err := hostBuildJSON(ctx, ex, "buildengine-scan-remote", spec.BuildEngineScanRemoteRequest{CacheDir: cacheDir, RepoPath: repoPath, Refs: refs}, &out); err != nil {
				return nil, err
			}
			return out, nil
		},
	}
}

// --- validate + prep legs ---

// validateProjectLeg runs the pre-build validation GATE via InvokeProvider(command:validate) — the
// plugin↔plugin form of the former host validateProjectForBuild (its comment named exit "K3").
func validateProjectLeg(ctx context.Context, ex *sdk.Executor, rr spec.ResolvedProjectRequest) error {
	params, err := json.Marshal(spec.ValidateProjectRequest{Dir: rr.Dir, IncludeDisabled: rr.IncludeDisabled})
	if err != nil {
		return err
	}
	res, err := ex.InvokeProvider(ctx, "command", "validate", sdk.OpValidate, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	var diags spec.Diagnostics
	if len(res) > 0 {
		if err := json.Unmarshal(res, &diags); err != nil {
			return fmt.Errorf("pre-build validation: decode diagnostics: %w", err)
		}
	}
	var msgs []string
	for _, it := range diags.Items {
		if it.Severity == "warning" {
			continue
		}
		msgs = append(msgs, it.Message)
	}
	switch len(msgs) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("validation error: %s", msgs[0])
	default:
		out := "validation errors:"
		for _, m := range msgs {
			out += "\n  " + m
		}
		return fmt.Errorf("%s", out)
	}
}

// renderSeamPrepLeg populates the render-seam-floor's CHEAP candy-scan-only Generator cache
// (host_build_render_seam.go's 2 remaining reverse-channel consumers + host_build_bake_plugins.go's
// emitBakedPlugins — none of which touch box-resolve data). The host-fs PREP itself
// (cleanStaleBuildDirs/writeContextIgnore/createRemoteCandyCopies/ensureCharlyBinaryFresh) runs
// plugin-side now (runHostFSPrep, host_prep.go, K3 host-prep move) — this leg is data-scan-only.
// Reply is error-only.
func renderSeamPrepLeg(ctx context.Context, ex *sdk.Executor, rr spec.ResolvedProjectRequest) error {
	return hostVoidLeg(ctx, ex, "buildengine-prep", rr)
}

// inspectUserLeg probes an external base image for a uid account via InvokeProvider(verb:oci)
// (oci_op=inspect-user) — the plugin-side resolveUserContext external-base branch.
func inspectUserLeg(ctx context.Context, ex *sdk.Executor, ref string, uid int) (spec.UserInfo, error) {
	params, err := json.Marshal(spec.ImageUserInput{Ref: ref, UID: uid})
	if err != nil {
		return spec.UserInfo{}, err
	}
	env, err := json.Marshal(map[string]string{"oci_op": "inspect-user"})
	if err != nil {
		return spec.UserInfo{}, err
	}
	res, err := ex.InvokeProvider(ctx, "verb", "oci", sdk.OpRun, params, env, sdk.InvokeProviderOpts{})
	if err != nil {
		return spec.UserInfo{}, err
	}
	var info spec.UserInfo
	if len(res) > 0 {
		if err := json.Unmarshal(res, &info); err != nil {
			return spec.UserInfo{}, fmt.Errorf("oci inspect-user: decode reply: %w", err)
		}
	}
	return info, nil
}

// --- envelope assembler (plugin seams) ---

// projectResolvedProjectLeg calls the SHARED loaderkit.ProjectResolvedProject assembler (U2) with
// PLUGIN-supplied ResolveProjectSeams: ResolveBox is pure buildkit; ResolveResources rides
// InvokeProvider(kind:resource); FillNamespacedBoxes is delivered as PRE-computed inputs from the
// buildengine-namespaced host leg (it embeds a nested scan+render-prep the plugin can't cheaply run);
// ComputeIntermediates/ShouldIncludeDisabled/ExternalizedBuilders are pure. preResolvedBoxes carries
// the render-prep caches so the envelope preserves them.
func projectResolvedProjectLeg(ctx context.Context, ex *sdk.Executor, cfg *spec.Config, layers map[string]spec.CandyReader, uf *loaderkit.UnifiedFile, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, initCfg *buildkit.InitConfig, dir, version, calver string, includeDisabled bool, preResolvedBoxes map[string]*buildkit.ResolvedBox) (*spec.ResolvedProject, error) {
	includeNames := map[string]bool{}
	seams := loaderkit.ResolveProjectSeams{
		ResolveBox: func(c *spec.Config, name, cv, d string) (*buildkit.ResolvedBox, error) {
			return buildkit.ResolveBox(c, name, cv, d, buildkit.ResolveOpts{IncludeDisabled: includeDisabled, DistroCfg: distroCfg, BuilderCfg: builderCfg})
		},
		FillNamespacedBoxes: func(_ *loaderkit.UnifiedFile, _ *buildkit.InitConfig, prefix, cv, d string, rp *spec.ResolvedProject, _ map[*loaderkit.UnifiedFile]bool) {
			if prefix != "" {
				return // the host leg does the full namespace recursion; only the root call dispatches
			}
			var partial spec.ResolvedProject
			if err := hostBuildJSON(ctx, ex, "buildengine-namespaced", spec.BuildResolveRequest{Dir: d, Tag: cv, IncludeDisabled: includeDisabled}, &partial); err != nil {
				return // best-effort/additive, matching the host fill's tolerance
			}
			mergeNamespacedInto(rp, &partial)
		},
		ResolveResources: func(u *loaderkit.UnifiedFile) map[string]*spec.ResolvedResource {
			return loaderkit.ResolvePluginKindViaPlugin(u, "resource", resolveResourceLeg(ctx, ex))
		},
		ShouldIncludeDisabled: func(name string) bool {
			if !includeDisabled {
				return false
			}
			if len(includeNames) == 0 {
				return true
			}
			return includeNames[name]
		},
		ComputeIntermediates: func(boxes map[string]*buildkit.ResolvedBox, l map[string]spec.CandyReader, c *spec.Config, tag string) (map[string]*buildkit.ResolvedBox, error) {
			return deploykit.ComputeIntermediates(boxes, l, intermediateDefaults(c), tag)
		},
		ExternalizedBuilders: buildkit.ExternalizedBuilders,
	}
	return loaderkit.ProjectResolvedProject(cfg, layers, uf, distroCfg, builderCfg, initCfg, dir, version, calver, seams, nil, preResolvedBoxes)
}

// mergeNamespacedInto folds the pre-computed namespaced boxes/candies into rp additively (never
// overwriting an already-present entry — the same tolerance the host fillNamespacedBoxes has).
func mergeNamespacedInto(rp, partial *spec.ResolvedProject) {
	for k, v := range partial.Boxes {
		if _, exists := rp.Boxes[k]; exists {
			continue
		}
		if rp.Boxes == nil {
			rp.Boxes = map[string]spec.ResolvedBoxView{}
		}
		rp.Boxes[k] = v
	}
	for k, v := range partial.CandyModels {
		if _, exists := rp.CandyModels[k]; exists {
			continue
		}
		if rp.CandyModels == nil {
			rp.CandyModels = map[string]spec.CandyModel{}
			rp.Candies = map[string]spec.CandyView{}
		}
		rp.CandyModels[k] = v
		rp.Candies[k] = partial.Candies[k]
	}
}
