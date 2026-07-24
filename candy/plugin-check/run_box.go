package check

// run_box.go — K1-unblock W3 Unit B: pluginCheckRunBox, the plugin-resident port of the former
// core "box" mode arm of the "check-run" HostBuild dispatch (deleted with this move) — the FIRST
// of six arms to move, chosen as the simplest/most
// self-contained: no deploy/venue-tree resolution, no cross-deployment addressing, just a
// disposable build-context container). Ported unchanged in substance — every call this arm
// makes was ALREADY sdk-portable (kit.ResolveRuntime/ResolveLocalImageRef,
// deploykit.ExtractMetadata/CheckBoxContainerChain, kit.ResolveCheckVarsBuild/RunPlan); the
// ONLY core-only piece was newCheckRunner, replaced by this package's own
// newPluginCheckRunner (plugin_runner.go) built on the W3 Unit B InvokeProvider mechanism.

import (
	"context"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// pluginCheckRunBox runs a pure-box check: a disposable container built from the image,
// build-scope steps only (RunModeBox). Mirrors the former in-core engine exactly (minus nothing —
// the CLI parse + reporters already live here, in check_cmd.go), so the reply's []StepResult
// formats byte-identically to the former in-core arm.
func pluginCheckRunBox(ex *sdk.Executor, ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	imageRef, err := kit.ResolveLocalImageRef(rt.RunEngine, req.Image)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	meta, err := deploykit.ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		return kit.CheckRunReply{Image: imageRef, NoSteps: true}, nil
	}

	// R44 Option A: ONE persistent container + per-step `podman exec` (checkBoxContainerChain),
	// not a `podman run --rm` per step — O(N)→O(1) container setups.
	executor, teardown, err := deploykit.CheckBoxContainerChain(rt.RunEngine, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	defer teardown()
	resolver := kit.ResolveCheckVarsBuild(meta)
	env, hasRuntime := pluginResolverEnv(resolver)

	// The box-context venue is the SAME single-hop deploykit.ContainerChain shape (via
	// CheckBoxContainerChain) that resolveCheckVenue produces for a live pod — round-trips
	// through the "container" VenueDescriptor kind (W3 Unit B's sdk leg) exactly the same way.
	var venueDesc *spec.VenueDescriptor
	if d := kit.DescriptorFromExecutor(executor); d.Kind != "" {
		venueDesc = &d
	}
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "box",
		Distros:   meta.Distro,
		VenueKind: executor.Kind(),
	}, venueDesc, kit.RunnerConfig{
		Exec:       executor,
		Mode:       kit.ModeBox,
		Env:        env,
		HasRuntime: hasRuntime,
		Distros:    meta.Distro,
		VerifyOnly: true,
	})

	stepResults := kit.RunPlan(ctx, runner, meta.Description, false)
	return kit.CheckRunReply{Image: imageRef, Steps: stepResults}, nil
}

// pluginResolverEnv mirrors charly/checkrun.go's resolverEnv exactly (trivial, no core-only
// dependency: a nil-guard over kit.CheckVarResolver's two exported fields).
func pluginResolverEnv(res *kit.CheckVarResolver) (map[string]string, bool) {
	if res == nil {
		return nil, false
	}
	return res.Env, res.HasRuntime
}
