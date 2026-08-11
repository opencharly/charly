package fleet

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// compile.go — the deploy-COMPILE core of command:fleet (compilePlansForRequest). It re-hydrates
// the resolved-project envelope itself via InvokeProvider("build","project") (the established seam —
// it does NOT receive the whole project in the request), resolves the per-node SELECTION off that
// envelope (box_select.go / candy_select.go — the plugin resolves the box view + candy order + the
// synthetic vm/host box itself, K4 unit B), runs the builder deploy-time pre-pass
// (builder_preresolve.go), loops deploykit.BuildDeployPlan, and returns []*spec.InstallPlan.
//
// K4-C shape-2: the COMPILE runs entirely PLUGIN-SIDE. The plugin's own tree-walk (walk.go
// dispatchOne → dispatch.go compileNodePlans) calls compilePlansForRequest IN-PROC — no OpCompile
// round-trip — killing the former plugin→host→plugin double-bounce. OpCompile (compileDeployPlans)
// stays as the WIRE leg (the parity test exercises it, and an out-of-process placement would use
// it), calling the SAME shared compilePlansForRequest (R3 — ONE compile, two entry points). The
// pure compiler (BuildDeployPlan) is a kind-blind MECHANISM in sdk/deploykit; this file is the thin
// envelope↔plugin glue that keeps the compile loop out of charly/ core (the kernel/plugin boundary
// law). IMPORT-PURITY: imports ONLY github.com/opencharly/sdk (spec/deploykit/proto are subpackages
// of the sdk module); never charly/.

// runFleetCompile serves command:fleet's Invoke(OpCompile): recover the executor, stash the
// reverse-channel handle, decode the per-node selection, compile via the plugin, and return the
// marshalled DeployCompileReply.
func runFleetCompile(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("fleet compile: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	return compileDeployPlans(ctx, exec, req)
}

// compileDeployPlans decodes the OpCompile request, runs the SHARED in-proc compiler
// (compilePlansForRequest — the SAME function the plugin's own dispatchOne walk calls IN-PROC, with
// NO OpCompile round-trip), and returns the compiled plans as a marshalled DeployCompileReply. Only
// the wire decode + view projection live here; the compile itself is the shared fn below (R3 — ONE
// compile implementation, two entry points: the OpCompile wire leg and the in-proc dispatchOne leg).
func compileDeployPlans(ctx context.Context, exec *sdk.Executor, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var r spec.DeployCompileRequest
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &r); err != nil {
			return nil, fmt.Errorf("fleet compile: decode request: %w", err)
		}
	}
	plans, err := compilePlansForRequest(ctx, exec, r)
	if err != nil {
		return nil, err
	}

	// Project each plan to its InstallPlanView wire form for the host to re-materialize.
	views := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		views = append(views, deploykit.WireView(p))
	}
	plansJSON, err := json.Marshal(views)
	if err != nil {
		return nil, fmt.Errorf("fleet compile: marshal plans: %w", err)
	}

	order := make([]string, 0, len(plans))
	for _, p := range plans {
		if p.Candy != "" {
			order = append(order, p.Candy)
		}
	}
	reply := spec.DeployCompileReply{
		PlansJSON: plansJSON,
		Base:      r.BoxView.Name,
		CandySet:  order,
	}
	replyJSON, err := json.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("fleet compile: marshal reply: %w", err)
	}
	return &pb.InvokeReply{ResultJson: replyJSON}, nil
}

// compilePlansForRequest is the SHARED in-proc deploy compiler (K4-C shape-2 extraction): it
// re-hydrates the resolved-project envelope + the per-node selection, runs the builder deploy-time
// pre-pass (FLOOR-SLIM-proper Unit-8 — see builder_preresolve.go), loops deploykit.BuildDeployPlan,
// and returns the compiled []*spec.InstallPlan. BOTH the OpCompile wire handler (compileDeployPlans)
// AND the plugin's own tree-walk (walk.go dispatchOne → compileNodePlans) call it — the latter
// IN-PROC with no OpCompile round-trip, killing the former plugin→host→plugin double-bounce.
func compilePlansForRequest(ctx context.Context, exec *sdk.Executor, r spec.DeployCompileRequest) ([]*spec.InstallPlan, error) {
	// Fetch the resolved-project envelope via the established InvokeProvider("build","project") seam.
	// ExtraCandyRefs (an --add-candy / add_candy: ref this compile call's own candy set was
	// widened with, host-side) widens the ENVELOPE's scan the SAME way, so a remote add-candy
	// (never reachable from any box's image closure) is actually present in rp.Candies/
	// rp.CandyModels below — RCA'd K1-alpha regression: the host's own scan (scanCandiesForRef)
	// and this envelope re-fetch used to run independently, so a remote add-candy resolved
	// host-side never reached here at all.
	// IncludeDisabled (BOX-REF shape only): mirrors the OLD host ResolveBox(cfg, ref.Name, …) call,
	// which never checked IsEnabled at all — enabled-filtering is a ResolveAllBox/listing concern,
	// not a by-name-resolve one. Without this, an explicitly-named `enabled: false` box would be
	// ABSENT from rp.Boxes (the envelope's box loop skips disabled boxes by default) even though
	// the OLD code resolved it fine. Zero cost today (zero disabled boxes exist repo-wide) —
	// future-proofing, not a live behavior change.
	rpPtr, err := fetchResolvedProject(r.Dir, r.ExtraCandyRefs, r.BoxRef != "" || r.BaseBoxRef != "")
	if err != nil {
		return nil, err
	}
	rp := *rpPtr

	// Re-hydrate the host-computed HostContext (the MachineVenue probe + glibc + builder-image
	// override — vmshared.DetectHostDistro is sdk-portable, so the plugin computes these in
	// dispatchOne; the OpCompile parity path passes a pre-built one).
	var hostCtx deploykit.HostContext
	if len(r.HostContextJSON) > 0 {
		if err := json.Unmarshal(r.HostContextJSON, &hostCtx); err != nil {
			return nil, fmt.Errorf("fleet compile: decode host context: %w", err)
		}
	}

	// Resolve the MachineVenue active init system plugin-side off the envelope's rp.Init (K4-C
	// shape-2: the former host-side preresolveActiveInitInto, which ran LoadBuildConfigForBox →
	// initCfg → resolveActiveInitByName, is RETIRED — rp.Init IS initCfg.Init (resolve_project.go),
	// so the plugin resolves it directly here without a host round-trip). A machine venue runs the
	// MACHINE'S OWN init; resolve "systemd" BY NAME with an existence check, hard-erroring if absent
	// (byte-identical to the former host preresolveActiveInitInto/resolveActiveInitByName contract:
	// "systemd" is the only init a machine venue resolves today). A container-image compile
	// (MachineVenue==false) is a no-op. Guarded on ActiveInitName=="" so a caller that already
	// resolved it (none in production post-shape-2; belt-and-suspenders) is never overwritten.
	if hostCtx.MachineVenue && hostCtx.ActiveInitName == "" {
		def, ok := rp.Init["systemd"]
		if !ok || def == nil {
			return nil, fmt.Errorf("fleet compile: machine-venue deploy requires the \"systemd\" init system, but the resolved-project envelope declares no init.systemd entry")
		}
		hostCtx.ActiveInitName = "systemd"
		hostCtx.ActiveInit = def
	}

	// Three selection SHAPES (K4 unit B): CandyRef set → the plugin resolves the standalone-candy
	// order + synthetic box itself from the envelope (candy_select.go); BoxRef set → the plugin
	// resolves the primary box view + candy order itself from the envelope (box_select.go);
	// otherwise the BOX-VIEW shape (unchanged, add_candy-on-pod/kubernetes) trusts the host-provided
	// BoxView/Order.
	var img *buildkit.ResolvedBox
	var order []string
	switch {
	case r.CandyRef != "" && r.BaseBoxRef != "":
		// ADD-CANDY-ON-BOX shape (K4 box-half completion): the overlay candy_ref compiled against
		// the primary base image base_box_ref, both resolved off the envelope (box_select.go).
		var selErr error
		order, img, selErr = resolveAddCandyOnBoxSelection(&rp, r)
		if selErr != nil {
			return nil, fmt.Errorf("fleet compile: %w", selErr)
		}
		order = deploykit.PruneContainerInitForSystemd(order, hostCtx)
	case r.CandyRef != "":
		var selErr error
		order, img, selErr = resolveCandySelection(ctx, exec, &rp, r)
		if selErr != nil {
			return nil, fmt.Errorf("fleet compile: %w", selErr)
		}
		// Mirrors the OLD host compileCandySelection/compileBoxSelection's pruneContainerInitForSystemd
		// call — the SAME pure sdk/deploykit function (R3), applied here because order is now
		// plugin-computed for this shape (the BOX-VIEW shape still prunes host-side before sending Order).
		order = deploykit.PruneContainerInitForSystemd(order, hostCtx)
	case r.BoxRef != "":
		var selErr error
		img, order, selErr = resolveBoxSelection(&rp, r)
		if selErr != nil {
			return nil, fmt.Errorf("fleet compile: %w", selErr)
		}
		order = deploykit.PruneContainerInitForSystemd(order, hostCtx)
	default:
		img = deploykit.NewSpecResolvedBox(r.BoxView, rp.Distro, rp.Builder)
		order = r.Order
	}

	// Loop the pure compiler over the FINAL pruned candy order. Re-hydrate every candy's
	// CandyModel ONCE up front (shared by the builder pre-pass below AND the compile loop, R3 —
	// no double NewSpecCandyModel construction).
	candyModels := make(map[string]spec.CandyReader, len(order))
	for _, name := range order {
		cm, cmOk := rp.CandyModels[name]
		cv, cvOk := rp.Candies[name]
		if !cmOk || !cvOk {
			return nil, fmt.Errorf("fleet compile: candy %q not in resolved-project envelope (order=%v)", name, order)
		}
		candyModels[name] = deploykit.NewSpecCandyModel(cm, cv)
	}

	// The deploy-time builder pre-pass (FLOOR-SLIM-proper Unit-8): populate
	// hostCtx.BuilderContext BEFORE the pure compile loop below, using exec.InvokeProvider
	// against the SAME builder plugins the host's own connect step (charly-core's
	// ensureBuildersConnected) already build-connected. Replaces the host pre-populating this
	// field on r.HostContextJSON.
	builderCtx, err := preresolveBuilderContexts(ctx, exec, order, candyModels, rp.ExternalizedBuilders, img)
	if err != nil {
		return nil, fmt.Errorf("fleet compile: builder pre-pass: %w", err)
	}
	if builderCtx != nil {
		hostCtx.BuilderContext = builderCtx
	}
	plans := make([]*spec.InstallPlan, 0, len(order))
	for _, name := range order {
		p, err := deploykit.BuildDeployPlan(ctx, exec, candyModels[name], img, hostCtx)
		if err != nil {
			return nil, fmt.Errorf("fleet compile: BuildDeployPlan(%s): %w", name, err)
		}
		if r.Tag != "" && p.Version == "" {
			p.Version = r.Tag
		}
		plans = append(plans, p)
	}
	return plans, nil
}

// fetchResolvedProject fetches + decodes the resolved-project envelope over the established
// InvokeProvider("build","project") seam. Shared (R3) by compilePlansForRequest (per-shape, with the
// shape's ExtraCandyRefs/IncludeDisabled) and the walk's per-node ref classification
// (compileNodePlans → resolveDeployRef, off rp.Boxes/rp.Candies). ExtraCandyRefs widens the scan so
// a REMOTE add-candy (never reachable from a box's image closure) is present in rp.Candies;
// includeDisabled mirrors the OLD host ResolveBox's never-check-IsEnabled by-name resolve.
func fetchResolvedProject(dir string, extraCandyRefs []string, includeDisabled bool) (*spec.ResolvedProject, error) {
	if cmdExec == nil {
		return nil, fmt.Errorf("fleet: no host reverse channel (command not compiled-in?)")
	}
	envReq, err := json.Marshal(spec.ResolvedProjectRequest{Dir: dir, ExtraCandyRefs: extraCandyRefs, IncludeDisabled: includeDisabled})
	if err != nil {
		return nil, fmt.Errorf("fleet: marshal resolved-project request: %w", err)
	}
	envJSON, err := cmdExec.InvokeProvider(cmdCtx, "build", "project", sdk.OpResolve, envReq, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return nil, fmt.Errorf("fleet: fetch resolved-project envelope: %w", err)
	}
	var rp spec.ResolvedProject
	if err := json.Unmarshal(envJSON, &rp); err != nil {
		return nil, fmt.Errorf("fleet: decode resolved-project envelope: %w", err)
	}
	return &rp, nil
}
