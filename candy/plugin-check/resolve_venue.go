package check

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// resolve_venue.go — the host-serving venue-CLASSIFICATION capability (verb:check-resolve,
// #118 check broker-envelope-out). The host's floor reverse-legs (endpoint / graphics / gRPC
// exec-attach) call this over InvokeProvider(OpResolve) so the kind-decode — checkVmTarget /
// checkLocalTarget branching on the stamped venue trait, ALREADY owned here in venue.go — lives in
// this check-capability plugin, never as an in-core classifier duplicate. The reply is the wire-safe
// projection of the resolved CheckVenue: the generic spec.VenueDescriptor (the host re-materializes a
// live DeployExecutor from it — a live executor never crosses the wire) plus the scalar venue facts
// the legs consume. resolveCheckVenue itself reaches the merged deploy tree via this package's own
// resolvedProject → InvokeProvider("build","project"), so the request carries only name + instance.

// resolveVenueForHost serves one verb:check-resolve OpResolve: classify name/instance's venue and
// return its wire-safe projection. It recovers the reverse-channel executor the host threaded (the
// in-proc ContextWithExecutor path) so resolveCheckVenue's resolvedProject leg can reach the host.
func resolveVenueForHost(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in spec.CheckVenueResolveRequest
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-check: decode check-resolve request: %w", err)
		}
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-check: check-resolve reverse-channel executor: %w", err)
	}
	dir, _ := os.Getwd()
	venue, err := resolveCheckVenue(exec, ctx, dir, in.Name, in.Instance)
	if err != nil {
		return nil, err
	}
	reply := spec.CheckVenueResolveReply{
		Descriptor: venue.Descriptor,
		Kind:       venue.Kind,
		Engine:     venue.Engine,
		Name:       venue.Name,
		VMName:     venue.VMName,
		// A dotted name is the ONLY shape resolveCheckVenue resolves through a multi-hop
		// deploykit.ResolveDeployChain (all three dotted branches); the single-hop descriptor
		// cannot express that chain, so the host rebuilds it via the SAME kind-blind
		// ResolveDeployChain when Nested (falling back to the descriptor if the walk can't, exactly
		// as resolveCheckVenue's own dotted-vm branch degrades to the plain SSHExecutor).
		Nested: strings.Contains(in.Name, "."),
	}
	out, err := json.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("plugin-check: marshal check-resolve reply: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}
