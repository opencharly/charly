package bundle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
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
	envReq, err := json.Marshal(spec.ResolvedProjectRequest{Dir: r.Dir})
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

	// Re-hydrate the resolved box from its envelope view + the project vocab maps.
	img := deploykit.NewSpecResolvedBox(r.BoxView, rp.Distro, rp.Builder)

	// Re-hydrate the host-side HostContext.
	var hostCtx deploykit.HostContext
	if len(r.HostContextJSON) > 0 {
		if err := json.Unmarshal(r.HostContextJSON, &hostCtx); err != nil {
			return nil, fmt.Errorf("bundle compile: decode host context: %w", err)
		}
	}

	// Loop the pure compiler over the host-provided FINAL pruned candy order. Re-hydrate every
	// candy's CandyModel ONCE up front (shared by the builder pre-pass below AND the compile
	// loop, R3 — no double NewSpecCandyModel construction).
	order := r.Order
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
		p, err := deploykit.BuildDeployPlan(candyModels[name], img, hostCtx)
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
