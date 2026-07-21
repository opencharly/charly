package main

// bundle_compile_seam.go — the K4-B host-side deploy-COMPILE seam. The InstallPlan compile loop
// (BuildDeployPlan) moved out of charly/ core into candy/plugin-bundle (the command:bundle plugin's
// OpCompile leg); the kernel/plugin boundary law puts a kind-blind MECHANISM that is NOT one of the
// four in-core M's into a plugin. The host now ONLY computes the per-node SELECTION — the resolved
// box (authored OR synthetic) projected to a spec.ResolvedBoxView, the FINAL pruned candy order,
// the host-side deploykit.HostContext incl. the preresolved BuilderContext — and Invokes the plugin's
// OpCompile with a spec.DeployCompileRequest. The plugin re-hydrates the resolved-project envelope
// itself via HostBuild("resolved-project") (the established #67 seam — it does NOT receive the whole
// project in the request), re-hydrates the box vocab via deploykit.NewSpecResolvedBox and each candy
// model via deploykit.NewSpecCandyModel, loops deploykit.BuildDeployPlan over the host-provided
// order, and returns []spec.InstallPlanView. The host re-materializes []*InstallPlan from the views
// via deploykit.PlanFromView. The compile CALL SITE no longer lives in core (R5: the old
// compilePlans/compileBoxPlans/compileCandyPlans/compileCandyPlansWithContext loop bodies are deleted).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// compileViaPlugin invokes the command:bundle plugin's OpCompile with the per-node selection,
// threading an in-proc reverse channel so the plugin can fetch the resolved-project envelope via
// HostBuild("resolved-project"). The plugin re-hydrates + loops deploykit.BuildDeployPlan +
// projects []InstallPlanView; the host re-materializes []*InstallPlan. command:bundle is compiled-in
// (in-proc), so the reverse server carries no venue executor — HostBuild("resolved-project") needs
// only the host build-engine context (which hostBuildResolvedProject ignores, reading req.Dir),
// exactly like dispatchBuild's in-proc reverse channel.
func (c *deployAddCmd) compileViaPlugin(req spec.DeployCompileRequest) ([]*deploykit.InstallPlan, error) {
	prov, ok := providerRegistry.resolve(ClassCommand, "bundle")
	if !ok {
		return nil, fmt.Errorf("compile: command:bundle provider not loaded (candy/plugin-bundle must be compiled in via compiled_plugins:)")
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("compile: marshal request: %w", err)
	}
	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	res, err := prov.Invoke(ctx, &Operation{Reserved: "bundle", Op: sdk.OpCompile, Params: reqJSON})
	if err != nil {
		return nil, fmt.Errorf("compile: bundle plugin OpCompile: %w", err)
	}
	if res == nil || len(res.JSON) == 0 {
		return nil, fmt.Errorf("compile: bundle plugin OpCompile returned no reply")
	}
	var reply spec.DeployCompileReply
	if err := json.Unmarshal(res.JSON, &reply); err != nil {
		return nil, fmt.Errorf("compile: decode reply: %w", err)
	}
	var views []spec.InstallPlanView
	if err := json.Unmarshal(reply.PlansJSON, &views); err != nil {
		return nil, fmt.Errorf("compile: decode plans: %w", err)
	}
	plans := make([]*deploykit.InstallPlan, 0, len(views))
	for _, v := range views {
		p, err := deploykit.PlanFromView(v)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p) // charly InstallPlan = deploykit.InstallPlan = spec.InstallPlan
	}
	return plans, nil
}

// preresolveActiveInitInto preresolves a MachineVenue compile's active init system ONCE per
// whole-deploy (alongside preresolveBuildersInto's builder pre-pass), so compileServiceSteps
// (install_build_services.go) never re-derives it per-candy nor falls back to the
// container-oriented InitConfig.ResolveInitSystem auto-detect heuristic — proven WRONG for a
// machine venue by a live probe (2026-07-20): a plain custom-exec service entry matches BOTH the
// systemd and supervisord ServiceSchema (both carry a non-empty ServiceTemplate), so auto-detect
// cannot disambiguate, and its bootc-oriented tie-break ("prefer supervisord for container
// images") would silently flip a real host/vm/local deploy to supervisord. A machine venue runs
// the MACHINE'S OWN init — resolve initCfg.Init["systemd"] BY NAME with an existence check,
// hard-erroring if absent rather than silently rendering no unit text (the original bug:
// compileServiceSteps' lazy loadSystemd() swallowed a missing entry with no error at all). The
// declared-venue-init trait (a vm/local Descent trait naming its own init system) is the tracked
// generic exit that eventually replaces this by-name lookup; until it lands, "systemd" is the
// only init a machine venue resolves. A no-op (returns hostCtx unchanged) for a container-image
// compile (hostCtx.MachineVenue == false).
func preresolveActiveInitInto(hostCtx deploykit.HostContext, dir string) (deploykit.HostContext, error) {
	if !hostCtx.MachineVenue {
		return hostCtx, nil
	}
	_, _, initCfg, err := LoadBuildConfigForBox(dir)
	if err != nil {
		return hostCtx, err
	}
	name, def, err := resolveActiveInitByName(initCfg)
	if err != nil {
		return hostCtx, err
	}
	hostCtx.ActiveInitName = name
	hostCtx.ActiveInit = def
	return hostCtx, nil
}

// resolveActiveInitByName is the pure by-name, existence-checked lookup preresolveActiveInitInto
// wraps around LoadBuildConfigForBox — split out so it is directly unit-testable against a
// hand-built *InitConfig, without a full project fixture. Only "systemd" is resolved today (every
// machine venue is systemd in practice, per pruneContainerInitForSystemd's pre-existing
// supervisord-exclusion for MachineVenue candy order); a future exclusive/machine-venue substrate
// declaring a different init names it via the tracked declared-venue-init trait, never a second
// hardcoded string here.
func resolveActiveInitByName(initCfg *InitConfig) (string, *ResolvedInit, error) {
	if initCfg == nil {
		return "", nil, fmt.Errorf("machine-venue deploy requires the \"systemd\" init system, but the project's build vocabulary declares no init: section at all")
	}
	def, ok := initCfg.Init["systemd"]
	if !ok || def == nil {
		return "", nil, fmt.Errorf("machine-venue deploy requires the \"systemd\" init system, but no init.systemd entry is declared in the build vocabulary")
	}
	return "systemd", def, nil
}

// compileSelectionViaPlugin is the ONE per-unit helper: project the resolved box, marshal the
// host-side HostContext, build the DeployCompileRequest, and re-materialize the plans. tag is the
// image CalVer pin (for the plan Version field when the candy carries no version).
func (c *deployAddCmd) compileSelectionViaPlugin(dir string, boxView spec.ResolvedBoxView, order []string, hostCtx deploykit.HostContext, tag string) ([]*deploykit.InstallPlan, error) {
	hostCtxJSON, err := json.Marshal(hostCtx)
	if err != nil {
		return nil, fmt.Errorf("compile: marshal host context: %w", err)
	}
	return c.compileViaPlugin(spec.DeployCompileRequest{
		Dir:             dir,
		BoxView:         boxView,
		Order:           order,
		HostContextJSON: hostCtxJSON,
		Tag:             tag,
	})
}

// compileRefSelection dispatches a primary ref (box vs candy) to the plugin compile, mirroring the
// OLD compilePlans. Remote image refs are unsupported (unchanged). base/candySet are computed
// host-side (the host overrides base for candy refs to ref.Name, matching the OLD semantics — the
// plugin returns Base=boxView.Name, but candy-ref units keep ref.Name).
func (c *deployAddCmd) compileRefSelection(ref *DeployRef, cfg *Config, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, dir string) ([]*deploykit.InstallPlan, string, []string, error) {
	if ref.Source == RefSourceRemote && ref.Kind == RefKindBox {
		return nil, "", nil, fmt.Errorf("remote image refs are not supported by bundle add (ref=%s)", ref.Raw)
	}
	if ref.Kind == RefKindBox {
		return c.compileBoxSelection(ref, cfg, distroCfg, builderCfg, dir)
	}
	return c.compileCandySelection(ref, cfg, distroCfg, builderCfg, dir, nil)
}

// compileBoxSelection mirrors the OLD compileBoxPlans: resolve the box, scan candies, resolve the
// topological order, prune for systemd, preresolve builders, then compile via the plugin. The
// plugin receives only the NON-nil candies (the OLD loop skipped layers[name]==nil); candySet is
// the FULL systemd-pruned order (the OLD return value), preserving deployID/overlay provenance.
func (c *deployAddCmd) compileBoxSelection(ref *DeployRef, cfg *Config, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, dir string) ([]*deploykit.InstallPlan, string, []string, error) {
	_ = distroCfg
	_ = builderCfg
	img, err := ResolveBox(cfg, ref.Name, c.Tag, dir, ResolveOpts{})
	if err != nil {
		return nil, "", nil, err
	}
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return nil, "", nil, err
	}
	order, err := deploykit.ResolveCandyOrder(img.Candy, layers, nil)
	if err != nil {
		return nil, "", nil, err
	}
	hostCtx := c.compileHostContext()
	hostCtx, err = preresolveActiveInitInto(hostCtx, dir)
	if err != nil {
		return nil, "", nil, err
	}
	order = pruneContainerInitForSystemd(order, hostCtx)
	hostCtx, err = preresolveBuildersInto(hostCtx, cfg, dir, order, layers, img)
	if err != nil {
		return nil, "", nil, err
	}
	compileOrder := make([]string, 0, len(order))
	for _, name := range order {
		if layers[name] != nil {
			compileOrder = append(compileOrder, name)
		}
	}
	plans, err := c.compileSelectionViaPlugin(dir, projectResolvedBox(img), compileOrder, hostCtx, c.Tag)
	if err != nil {
		return nil, "", nil, err
	}
	return plans, img.Name, order, nil
}

// compileCandySelection mirrors the OLD compileCandyPlans (ctx==nil, a standalone candy deploy
// picking the synthetic host/VM image template) and compileCandyPlansWithContext (ctx!=nil, an
// add_candy compiled against a pod/k8s base image's context). base is ref.Name for BOTH (the OLD
// return value), NOT the plugin's reply Base (which is boxView.Name).
func (c *deployAddCmd) compileCandySelection(ref *DeployRef, cfg *Config, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, dir string, ctx *buildkit.ResolvedBox) ([]*deploykit.InstallPlan, string, []string, error) {
	layers, candyKey, err := c.scanCandiesForRef(ref, cfg, dir)
	if err != nil {
		return nil, "", nil, err
	}
	order, err := deploykit.ResolveCandyOrder([]string{candyKey}, layers, nil)
	if err != nil {
		return nil, "", nil, fmt.Errorf("resolving deps for %s: %w", ref.Raw, err)
	}
	var img *buildkit.ResolvedBox
	if ctx != nil {
		// add_candy on a pod/k8s deploy: compile against the base image's context.
		img = ctx
		if distroCfg != nil && img.DistroDef == nil {
			img.DistroDef = distroCfg.ResolveDistro(img.Distro)
		}
		if builderCfg != nil && img.BuilderConfig == nil {
			img.BuilderConfig = builderCfg
		}
	} else {
		// Standalone candy deploy: pick the synthetic image template matching the target so
		// `${USER}` AND the package format resolve correctly (the guest user + guest distro/format
		// for a VM target, the operator host's for everything else).
		if c.vmEntity != "" {
			if uf, ok, _ := LoadUnified(dir); ok && uf != nil && uf.VM != nil {
				if body, present := uf.VM[c.vmEntity]; present {
					if vmspec, err := resolveVmViaPlugin(body); err == nil && vmspec != nil {
						img = syntheticVmBox(vmspec, distroCfg)
					}
				}
			}
		}
		if img == nil {
			img = syntheticHostBox()
		}
		if distroCfg != nil {
			img.DistroDef = distroCfg.ResolveDistro(img.Distro)
		}
		if builderCfg != nil {
			img.BuilderConfig = builderCfg
		}
		if cfg != nil {
			img.Builder = buildkit.ResolveEffectiveBuilder(cfg, img.Name, img.Distro, img.Base, img.IsExternalBase, img.Builder)
		}
	}
	hostCtx := c.compileHostContext()
	hostCtx, err = preresolveActiveInitInto(hostCtx, dir)
	if err != nil {
		return nil, "", nil, err
	}
	order = pruneContainerInitForSystemd(order, hostCtx)
	hostCtx, err = preresolveBuildersInto(hostCtx, cfg, dir, order, layers, img)
	if err != nil {
		return nil, "", nil, err
	}
	plans, err := c.compileSelectionViaPlugin(dir, projectResolvedBox(img), order, hostCtx, c.Tag)
	if err != nil {
		return nil, "", nil, err
	}
	return plans, ref.Name, order, nil
}
