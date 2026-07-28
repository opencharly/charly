package bundle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// compile.go — the K4-B deploy-COMPILE leg of command:bundle. The host's deployAddCmd.compileNodePlans
// computes the per-node SELECTION (the resolved box projected to a spec.ResolvedBoxView, the FINAL
// pruned candy order, the host-side HostContext incl. the preresolved BuilderContext) and Invokes the
// bundle provider's OpCompile with a spec.DeployCompileRequest; this handler re-hydrates the
// resolved-project envelope itself via HostBuild("resolved-project") (the established seam — it does
// NOT receive the whole project in the request), re-hydrates the box vocab via
// deploykit.NewSpecResolvedBox and each candy model via deploykit.NewSpecCandyModel, loops
// deploykit.BuildDeployPlan over the host-provided order, projects each plan to its InstallPlanView,
// and returns []InstallPlanView. The host re-materializes []*InstallPlan from the views via
// deploykit.PlanFromView.
//
// The compile CALL SITE lives in the plugin (K4-B); the host only computes the selection +
// re-materializes. The pure compiler (BuildDeployPlan) is a kind-blind MECHANISM already in
// sdk/deploykit; this handler is the thin envelope↔plugin glue that moves the loop out of charly/
// core (the kernel/plugin boundary law: a kind-blind mechanism that is NOT one of the four in-core
// M's is a plugin). IMPORT-PURITY: imports ONLY github.com/opencharly/sdk (spec/deploykit/proto are
// subpackages of the sdk module); never charly/.

// runBundleCompile serves command:bundle's Invoke(OpCompile): recover the executor, stash the
// reverse-channel handle, decode the per-node selection, compile via the plugin, and return the
// marshalled DeployCompileReply.
func runBundleCompile(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("bundle compile: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	return compileDeployPlans(ctx, exec, req)
}

// compileDeployPlans re-hydrates the resolved-project envelope + the per-node selection, runs the
// builder deploy-time pre-pass (FLOOR-SLIM-proper Unit-8 — see builder_preresolve.go), loops
// deploykit.BuildDeployPlan, and returns the compiled plans as a marshalled DeployCompileReply.
func compileDeployPlans(ctx context.Context, exec *sdk.Executor, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var r spec.DeployCompileRequest
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &r); err != nil {
			return nil, fmt.Errorf("bundle compile: decode request: %w", err)
		}
	}

	// Fetch the resolved-project envelope via the established HostBuild("resolved-project") seam.
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
	envReq, err := json.Marshal(spec.ResolvedProjectRequest{Dir: r.Dir, ExtraCandyRefs: r.ExtraCandyRefs, IncludeDisabled: r.BoxRef != "" || r.BaseBoxRef != ""})
	if err != nil {
		return nil, fmt.Errorf("bundle compile: marshal envelope request: %w", err)
	}
	if cmdExec == nil {
		return nil, fmt.Errorf("bundle compile: no host reverse channel (command not compiled-in?)")
	}
	envJSON, err := cmdExec.HostBuild(cmdCtx, "resolved-project", envReq)
	if err != nil {
		return nil, fmt.Errorf("bundle compile: fetch resolved-project envelope: %w", err)
	}
	var rp spec.ResolvedProject
	if err := json.Unmarshal(envJSON, &rp); err != nil {
		return nil, fmt.Errorf("bundle compile: decode resolved-project envelope: %w", err)
	}

	// Re-hydrate the host-side HostContext (ALWAYS host-computed — unchanged for both
	// selection shapes; see #DeployCompileRequest's doc comment).
	var hostCtx deploykit.HostContext
	if len(r.HostContextJSON) > 0 {
		if err := json.Unmarshal(r.HostContextJSON, &hostCtx); err != nil {
			return nil, fmt.Errorf("bundle compile: decode host context: %w", err)
		}
	}

	// Three selection SHAPES (K4 unit B): CandyRef set → the plugin resolves the standalone-candy
	// order + synthetic box itself from the envelope (candy_select.go); BoxRef set → the plugin
	// resolves the primary box view + candy order itself from the envelope (box_select.go);
	// otherwise the BOX-VIEW shape (unchanged, add_candy-on-pod/k8s) trusts the host-provided
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
			return nil, fmt.Errorf("bundle compile: %w", selErr)
		}
		order = deploykit.PruneContainerInitForSystemd(order, hostCtx)
	case r.CandyRef != "":
		var selErr error
		order, img, selErr = resolveCandySelection(ctx, exec, &rp, r)
		if selErr != nil {
			return nil, fmt.Errorf("bundle compile: %w", selErr)
		}
		// Mirrors the OLD host compileCandySelection/compileBoxSelection's pruneContainerInitForSystemd
		// call — the SAME pure sdk/deploykit function (R3), applied here because order is now
		// plugin-computed for this shape (the BOX-VIEW shape still prunes host-side before sending Order).
		order = deploykit.PruneContainerInitForSystemd(order, hostCtx)
	case r.BoxRef != "":
		var selErr error
		img, order, selErr = resolveBoxSelection(&rp, r)
		if selErr != nil {
			return nil, fmt.Errorf("bundle compile: %w", selErr)
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
			return nil, fmt.Errorf("bundle compile: candy %q not in resolved-project envelope (order=%v)", name, order)
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
		return nil, fmt.Errorf("bundle compile: builder pre-pass: %w", err)
	}
	if builderCtx != nil {
		hostCtx.BuilderContext = builderCtx
	}
	plans := make([]*spec.InstallPlan, 0, len(order))
	for _, name := range order {
		p, err := deploykit.BuildDeployPlan(ctx, exec, candyModels[name], img, hostCtx)
		if err != nil {
			return nil, fmt.Errorf("bundle compile: BuildDeployPlan(%s): %w", name, err)
		}
		if r.Tag != "" && p.Version == "" {
			p.Version = r.Tag
		}
		plans = append(plans, p)
	}

	// Project each plan to its InstallPlanView wire form for the host to re-materialize.
	views := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		views = append(views, deploykit.WireView(p))
	}
	plansJSON, err := json.Marshal(views)
	if err != nil {
		return nil, fmt.Errorf("bundle compile: marshal plans: %w", err)
	}

	reply := spec.DeployCompileReply{
		PlansJSON: plansJSON,
		Base:      r.BoxView.Name,
		CandySet:  order,
	}
	replyJSON, err := json.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("bundle compile: marshal reply: %w", err)
	}
	return &pb.InvokeReply{ResultJson: replyJSON}, nil
}
