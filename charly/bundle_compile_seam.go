package main

// bundle_compile_seam.go — the K4-B/K4-unit-B host-side deploy-COMPILE seam. The InstallPlan
// compile loop (BuildDeployPlan) moved out of charly/ core into candy/plugin-bundle (the
// command:bundle plugin's OpCompile leg); the kernel/plugin boundary law puts a kind-blind
// MECHANISM that is NOT one of the four in-core M's into a plugin. The plugin re-hydrates the
// resolved-project envelope itself via HostBuild("resolved-project") (the established #67 seam
// — it does NOT receive the whole project in the request), loops deploykit.BuildDeployPlan over
// the resolved order, and returns []spec.InstallPlanView. The host re-materializes
// []*InstallPlan from the views via deploykit.PlanFromView.
//
// Three selection SHAPES share the ONE OpCompile request (see #DeployCompileRequest's doc
// comment, sdk/schema/seam.cue, for the authoritative discrimination):
//   - BOX-VIEW (compileCandyOnBoxSelection, add_candy on an ALREADY-RESOLVED base image,
//     ctx!=nil) — UNCHANGED: the host still resolves the base image + candy order + builder
//     pre-connect here (the base image itself is a separate host-side ResolveBox result with no
//     envelope-only path yet), projects a spec.ResolvedBoxView, and sends box_view+order.
//   - CANDY (compileStandaloneCandySelection, the target:local/vm standalone-candy shape, K4 unit
//     B candy-half) — the plugin resolves the candy order + synthetic box itself off the
//     envelope; the host sends only candy_ref/vm_entity.
//   - BOX-REF (compileBoxSelection, the primary pod/k8s image shape, K4 unit B box-half) — the
//     plugin resolves the box view + candy order itself off the envelope (rp.Boxes[box_ref] IS
//     the SAME ResolvedBoxView the host used to project host-side before this move); the host
//     sends only box_ref.
//
// R5: the old compilePlans/compileBoxPlans/compileCandyPlans/compileCandyPlansWithContext loop
// bodies, plus the CANDY/BOX-REF shapes' host-side selection computation (ResolveBox,
// ScanAllCandyWithConfig, deploykit.ResolveCandyOrder, the synthetic box constructors, the
// builder pre-connect) are ALL deleted for those two shapes — see compileStandaloneCandySelection
// / compileBoxSelection's own doc comments for why the builder pre-connect specifically is
// provably redundant (candy/plugin-bundle's preresolveBuilderContexts already S2-lazy-connects
// unconditionally), not merely relocated.

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
// whole-deploy (alongside preresolveBuildersInto's builder pre-pass), so
// deploykit.CompileServiceSteps never re-derives it per-candy nor falls back to the
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
func resolveActiveInitByName(initCfg *buildkit.InitConfig) (string, *ResolvedInit, error) {
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
// image CalVer pin (for the plan Version field when the candy carries no version). extraCandyRefs
// is the add_candy:/--add-candy ref this compile call's own candy set was widened with (nil for a
// primary image/box compile) — threaded through so the plugin's OWN resolved-project re-fetch
// widens the SAME way (RCA'd K1-alpha regression: a remote add-candy resolved host-side via
// scanCandiesForRef's synthetic-augmented scan never reached the plugin's independent envelope
// fetch, "candy not in resolved-project envelope").
func (c *deployAddCmd) compileSelectionViaPlugin(dir string, boxView spec.ResolvedBoxView, order []string, hostCtx deploykit.HostContext, tag string, extraCandyRefs []string) ([]*deploykit.InstallPlan, error) {
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
		ExtraCandyRefs:  extraCandyRefs,
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

// compileBoxSelection is the K4-unit-B box-half shape: a primary pod/k8s image deploy. No
// host-side ResolveBox, no ScanAllCandyWithConfig, no candy-order resolve, no builder
// pre-connect — candy/plugin-bundle's box_select.go resolves the box view + candy order itself
// off the SAME resolved-project envelope it already fetches for the compile loop (rp.Boxes[box_ref]
// IS the ResolvedBoxView the host used to build host-side, just read directly instead of
// re-derived — R3), and its ALREADY-EXISTING preresolveBuilderContexts (compile.go,
// unconditional for every OpCompile) already S2-lazy-connects the SAME externalized builder
// plugins ensureBuildersConnectedForOrder used to pre-connect host-side (verified live, K4 unit
// B: an exhaustive repo grep found zero pod/k8s box deploy needing a builder outside the calling
// project's own candy closure). Only the LoadUnified-coupled init-vocabulary lookup
// (preresolveActiveInitInto, for hostCtx) stays host, same as the candy-half.
func (c *deployAddCmd) compileBoxSelection(ref *DeployRef, cfg *Config, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, dir string) ([]*deploykit.InstallPlan, string, []string, error) {
	_ = cfg
	_ = distroCfg
	_ = builderCfg
	hostCtx := c.compileHostContext()
	hostCtx, err := preresolveActiveInitInto(hostCtx, dir)
	if err != nil {
		return nil, "", nil, err
	}
	hostCtxJSON, err := json.Marshal(hostCtx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("compile: marshal host context: %w", err)
	}
	plans, err := c.compileViaPlugin(spec.DeployCompileRequest{
		Dir:             dir,
		BoxRef:          ref.Name,
		HostContextJSON: hostCtxJSON,
		Tag:             c.Tag,
	})
	if err != nil {
		return nil, "", nil, err
	}
	// The compiled candy set for deployID/overlay provenance — mirrors compileStandaloneCandySelection's
	// same derivation: plans preserve the plugin's topo-sorted order (BuildDeployPlan loops `order`
	// in sequence), so reading each plan's own Candy name back reconstructs it without needing the
	// reply to separately carry CandySet.
	order := make([]string, 0, len(plans))
	for _, p := range plans {
		if p.Candy != "" {
			order = append(order, p.Candy)
		}
	}
	return plans, ref.Name, order, nil
}

// compileCandySelection dispatches to the box-context add_candy path (ctx != nil, UNCHANGED — an
// add_candy: on a pod/k8s base, compiled against the already-resolved base image) or the
// standalone-candy path (ctx == nil, K4 unit B — candy/plugin-bundle now resolves the candy
// order + synthetic host/vm box ITSELF, off the resolved-project envelope it already fetches; see
// compileStandaloneCandySelection). base is ref.Name for BOTH (the OLD return value), NOT the
// plugin's reply Base (which is boxView.Name).
func (c *deployAddCmd) compileCandySelection(ref *DeployRef, cfg *Config, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, dir string, ctx *buildkit.ResolvedBox) ([]*deploykit.InstallPlan, string, []string, error) {
	if ctx == nil {
		return c.compileStandaloneCandySelection(ref, dir)
	}
	return c.compileCandyOnBoxSelection(ref, cfg, distroCfg, builderCfg, dir, ctx)
}

// compileStandaloneCandySelection is the K4-unit-B candy-only shape: a `target: local`/`vm`/every
// external-substrate deploy with NO base image. No host-side candy scan, no synthetic-box
// construction, no builder pre-connect — the plugin does ALL of that itself off the SAME
// resolved-project envelope it already fetches for the compile loop (candy/plugin-bundle's
// candy_select.go), and its ALREADY-EXISTING builder pre-pass (preresolveBuilderContexts,
// compile.go) already S2-lazy-connects the SAME externalized builder plugins
// ensureBuildersConnectedForOrder used to pre-connect host-side — unconditionally, for every
// compile call, box or candy alike (verified: all 4 builder plugin candies for this repo live in
// its own candy/ dir, always in the calling project's own closure S2's Pass-1 scans; a
// cross-repo/box-submodule builder ref, if ever needed, has its OWN Pass-2 fallback via
// InvokeProviderOpts.ExtraRef — unused today, not a gap this move introduces). Only the
// LoadUnified-coupled init-vocabulary lookup (preresolveActiveInitInto, for hostCtx) stays host,
// since hostCtx.ActiveInitName/ActiveInit are ALWAYS host-computed for both selection shapes (see
// #DeployCompileRequest's doc comment).
func (c *deployAddCmd) compileStandaloneCandySelection(ref *DeployRef, dir string) ([]*deploykit.InstallPlan, string, []string, error) {
	hostCtx := c.compileHostContext()
	hostCtx, err := preresolveActiveInitInto(hostCtx, dir)
	if err != nil {
		return nil, "", nil, err
	}
	hostCtxJSON, err := json.Marshal(hostCtx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("compile: marshal host context: %w", err)
	}
	// c.vmEntity is threaded TOLERANTLY (RCA'd live via check-preempt-local): resolveVmEntity
	// sets it from node.From unconditionally, not only for a genuine vm cross-ref — a `local:`
	// node's kind:local template ref lands here too. The plugin tries it against its own
	// rp.Templates.VM and falls through to a plain host box on a miss (candy_select.go's
	// syntheticBoxForCandySelection) — never a hard requirement, matching the OLD host-side
	// syntheticVmBox call site's own tolerant lookup exactly.
	plans, err := c.compileViaPlugin(spec.DeployCompileRequest{
		Dir:             dir,
		CandyRef:        ref.Raw,
		VmEntity:        c.vmEntity,
		HostContextJSON: hostCtxJSON,
		Tag:             c.Tag,
		ExtraCandyRefs:  []string{ref.Raw},
	})
	if err != nil {
		return nil, "", nil, err
	}
	// The compiled candy set for deployID/overlay provenance — mirrors what the OLD
	// deploykit.ResolveCandyOrder([]string{candyKey}, layers, nil) call returned, read back off
	// each compiled plan's own Candy name (the SAME source compileBoxSelection's compileOrder
	// filter already trusts).
	order := make([]string, 0, len(plans))
	for _, p := range plans {
		if p.Candy != "" {
			order = append(order, p.Candy)
		}
	}
	return plans, ref.Name, order, nil
}

// compileCandyOnBoxSelection is the UNCHANGED add_candy-on-pod/k8s shape (ctx != nil): compile
// against the ALREADY-RESOLVED base image's context. Host-side selection (scan+order+builder
// pre-connect) is unavoidable here — ctx (the base image) is itself a host-resolved
// *buildkit.ResolvedBox from compileNodePlans' ResolveBox call, so there is no envelope-only path
// to it yet (a K4 box-half concern, out of this slice's scope).
func (c *deployAddCmd) compileCandyOnBoxSelection(ref *DeployRef, cfg *Config, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, dir string, ctx *buildkit.ResolvedBox) ([]*deploykit.InstallPlan, string, []string, error) {
	layers, candyKey, err := c.scanCandiesForRef(ref, cfg, dir)
	if err != nil {
		return nil, "", nil, err
	}
	order, err := deploykit.ResolveCandyOrder([]string{candyKey}, layers, nil)
	if err != nil {
		return nil, "", nil, fmt.Errorf("resolving deps for %s: %w", ref.Raw, err)
	}
	img := ctx
	if distroCfg != nil && img.DistroDef == nil {
		img.DistroDef = distroCfg.ResolveDistro(img.Distro)
	}
	if builderCfg != nil && img.BuilderConfig == nil {
		img.BuilderConfig = builderCfg
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
	// ref.Raw (this call's own candy — the add_candy: overlay target itself) widens the plugin's
	// OWN resolved-project re-fetch the SAME way scanCandiesForRef widened THIS scan above — a
	// REMOTE ref is never reachable from any box's image closure, so without this the plugin's
	// independent envelope fetch never discovers it (RCA'd K1-alpha regression: check-addcandy-pod/
	// check-stepkind-emit-pod, "candy not in resolved-project envelope").
	plans, err := c.compileSelectionViaPlugin(dir, deploykit.ProjectResolvedBox(img), order, hostCtx, c.Tag, []string{ref.Raw})
	if err != nil {
		return nil, "", nil, err
	}
	return plans, ref.Name, order, nil
}
