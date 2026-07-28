package main

// dispatch_build_ensure.go — the host-side dispatch helper for the build:ensure word (core-min
// wave 3, build-engine cluster relocation). Every former ensure-image / cross-engine-transfer
// caller now reaches the compiled-in candy/plugin-build's ensure-image drive through this ONE
// function, mirroring candy/plugin-box's dispatchBuild for the box/generate words — the SAME
// in-proc reverse-channel pattern (a bare executorReverseServer{}, since ensure-image touches only
// the HOST's own podman storage, never a live deploy venue).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// dispatchBuildEnsure ensures image is present in local podman storage, falling back to a local
// (or remote-cached) build when the identifier maps to a project charly.yml entry — the SAME
// contract the former core ensure-image helper offered, now served by the compiled-in
// build:ensure plugin word. dir "" lets the seam resolve the project from cwd (mirroring the
// former opportunistic cwd-based project load). buildEngine/runEngine "" let the plugin
// self-resolve the runtime (kit.ResolveRuntime) instead of using a caller-supplied hint — pass
// explicit values only when the caller already has a resolved *kit.ResolvedRuntime to honor (the
// transfer.go pod-config-ensure-image case).
func dispatchBuildEnsure(ctx context.Context, image, dir, buildEngine, runEngine string) error {
	prov, ok := providerRegistry.resolve(ClassBuild, "ensure")
	if !ok {
		return fmt.Errorf("build ensure dispatch: no build:ensure provider registered (candy/plugin-build must be compiled in via compiled_plugins:)")
	}
	params, err := marshalJSON(spec.BuildEnsureRequest{
		Image:       image,
		Dir:         dir,
		BuildEngine: buildEngine,
		RunEngine:   runEngine,
	})
	if err != nil {
		return err
	}
	ictx := sdk.ContextWithExecutor(ctx,
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	res, err := prov.Invoke(ictx, &Operation{Reserved: "ensure", Op: OpBuild, Params: params})
	if err != nil {
		return err
	}
	var reply spec.BuildEnsureReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return fmt.Errorf("build ensure dispatch: decode reply: %w", err)
		}
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}
