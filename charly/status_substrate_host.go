package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// status_substrate_host.go — the generic "status-substrate" F10 host-builder. K6 relocated the
// LAST core-side status business logic (charly/status_collector.go's Collector + the deploy-cone
// enrichment) into candy/plugin-substrate (status_flat.go, verb:status-fanout). This handler is
// now PURE generic dispatch — an M-mechanism wire forward, no status-specific logic — resolving
// the verb and threading the SAME in-proc reverse-channel executor the fan-out's vm/k8s legs need
// (HostBuild("resolved-project") / InvokeProvider("verb","libvirt",...)) to reach the host for
// themselves. The externalized `charly status` command plugin (candy/plugin-status) is unchanged
// by this move — it still drives this SAME "status-substrate" HostBuild seam by name.
const statusSubstrateBuilderKind = "status-substrate"

// hostBuildStatusSubstrate forwards the request to verb:status-fanout (candy/plugin-substrate)
// and returns its reply verbatim. No status business logic lives here.
func hostBuildStatusSubstrate(ctx context.Context, req spec.StatusSubstrateRequest, _ buildEngineContext) (spec.StatusSubstrateReply, error) {
	prov, ok := providerRegistry.resolve(ClassVerb, "status-fanout")
	if !ok {
		return spec.StatusSubstrateReply{}, fmt.Errorf("status-substrate: substrate plugin (verb:status-fanout) not registered — charly built without the plugin-substrate candy")
	}
	ctx = sdk.ContextWithExecutor(ctx, sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	return invokeTyped[spec.StatusSubstrateRequest, spec.StatusSubstrateReply](ctx, prov, "status-fanout", sdk.OpStatusCollectAll, req)
}

var _ = func() bool {
	registerHostBuilder(statusSubstrateBuilderKind, typedHostBuilder(statusSubstrateBuilderKind, hostBuildStatusSubstrate))
	return true
}()
