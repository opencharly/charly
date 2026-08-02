package main

import (
	"context"
	"fmt"
	"os"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// check_venue_resolve.go — the host-side generic seam that reaches plugin-check's venue
// CLASSIFICATION capability (verb:check-resolve, #118 check broker-envelope-out). The kind-decode
// (checkVmTarget / checkLocalTarget branching on the stamped venue trait) is CHECK CAPABILITY LOGIC
// that lives in candy/plugin-check (venue.go), NOT an in-core classifier — the boundary-law self-test
// forbids a floor file switching on a deploy kind. This host leg holds ZERO classification: it
// resolves the compiled-in plugin, threads an in-proc reverse channel so plugin-check's own
// resolvedProject → InvokeProvider("build","project") leg can reach the host, invokes OpResolve, and
// returns the wire-safe spec.CheckVenueResolveReply. Each caller then builds its live endpoint /
// executor from the returned GENERIC spec.VenueDescriptor via the kind-blind kit re-materialization
// (deploykit endpoint resolution / specexec.VenueFromDescriptor) — the same descriptor→executor mechanism
// candy/plugin-bundle's PrepareVenue dispatch already goes through. A thin generic host-forward +
// re-materialize seam serving the check reverse channel (floor-M).

// resolveCheckVenueReply classifies a check target's venue via plugin-check and returns the wire-safe
// projection. Threads an in-proc reverse-channel executor so the plugin's resolved-project leg reaches
// the host (the ContextWithExecutor idiom the status-substrate seam uses). A resolve failure (e.g. a
// non-running container) propagates verbatim — the plugin's resolveCheckVenue calls the SAME
// deploykit.ResolveContainer the core original did.
func resolveCheckVenueReply(name, instance string) (spec.CheckVenueResolveReply, error) {
	prov, ok := providerRegistry.resolve(ClassVerb, "check-resolve")
	if !ok {
		return spec.CheckVenueResolveReply{}, fmt.Errorf("check-resolve: plugin-check (verb:check-resolve) not registered — charly built without the plugin-check candy")
	}
	ctx := specexec.ContextWithExecutor(context.Background(), specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	return invokeTyped[spec.CheckVenueResolveRequest, spec.CheckVenueResolveReply](
		ctx, prov, "check-resolve", ops.OpResolve,
		spec.CheckVenueResolveRequest{Name: name, Instance: instance})
}

// checkVenueExecFromReply re-materializes a LIVE host DeployExecutor from the plugin's venue
// classification — the host half of the seam (a live executor never crosses the wire, so the plugin
// returns only the descriptor + nested marker and the host rebuilds the executor by the SAME
// generic mechanisms the core original used). A NESTED target rebuilds its N-hop chain host-side via
// the kind-blind specexec.ResolveDeployChain off the shared host merged-tree read
// (resolveMergedDeployTree, byte-identical to the deleted
// core resolveCheckVenue's own dotted-path branch), degrading to the single-hop descriptor when the
// walk cannot resolve (matching the plugin's own dotted-vm fallback); a non-nested target
// re-materializes directly via specexec.VenueFromDescriptor. Zero kind classification — the descriptor's
// generic transport word (container/ssh/shell) is all either path consults.
func checkVenueExecFromReply(reply spec.CheckVenueResolveReply, name string) (spec.DeployExecutor, error) {
	if reply.Nested {
		dir, _ := os.Getwd()
		if roots, _ := resolveMergedDeployTree(dir); roots != nil {
			if _, chain, chainErr := specexec.ResolveDeployChain(roots, name, specexec.ShellExecutor{}); chainErr == nil && chain != nil {
				return chain, nil
			}
		}
	}
	return specexec.VenueFromDescriptor(reply.Descriptor)
}
