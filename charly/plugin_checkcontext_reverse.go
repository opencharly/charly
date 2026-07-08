package main

import (
	"context"
	"net/http"

	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
)

// checkContextReverseServer is the host-side CheckContextService (F2): the reverse channel
// an OUT-OF-PROCESS host-coupled check verb (kit.CheckVerbProvider) dials for the
// CheckContext legs that cannot ride the env_json snapshot — HTTPDo (host-vantage HTTP) and
// AddBackground (host-side PID registration). It is served on the SAME go-plugin broker as
// executorReverseServer (so a kit verb reaches BOTH the venue, via ExecutorService, AND these
// legs). It holds a MINIMAL surface (the engine's base HTTP client + an addBg closure), NOT
// the live *Runner, so the broker-serving path stays class-generic (the Uniform API
// Invariant) — any check verb may use either RPC, no per-verb coupling.
type checkContextReverseServer struct {
	pb.UnimplementedCheckContextServiceServer
	httpBase          *http.Client                                // the engine's base HTTP client (default timeout); per-request policy applied per call
	addBg             func(pid int)                               // r.Scenario.AddBackground, nil when there is no scenario context
	resolveEp         func(port int) (string, error)              // r.resolveVerbEndpoint — venue→addr, forwards tracked on the Runner for post-Invoke teardown
	resolveGfx        func(kind string) (graphicsEndpoint, error) // r.resolveVerbGraphics — VM graphics (vnc/spice) endpoint, tunnel tracked on the Runner
	resolveClusterCtx func(cluster string) (string, error)        // r.resolveClusterContext — k8s cluster-profile -> kubeconfig context via the project loader
	resolveImgLabel   func(label string) (string, error)          // r.resolveImageLabel — one raw OCI label off the deployment image
}

// HTTPDo issues the request from the host's network namespace via the SHARED host HTTP-do
// path (doHTTPRequest — the SAME builder the in-proc runnerCheckContext.HTTPDo uses, R3) and
// returns status/body/header-blob. A transport-level failure rides the reply error field (the
// RPC itself succeeds), like RunReply/CaptureReply.
func (s *checkContextReverseServer) HTTPDo(ctx context.Context, req *pb.HTTPDoRequest) (*pb.HTTPDoReply, error) {
	resp, err := doHTTPRequest(ctx, s.httpBase, kit.HTTPRequest{
		Method:            req.GetMethod(),
		URL:               req.GetUrl(),
		Body:              req.GetBody(),
		Headers:           req.GetHeaders(),
		Timeout:           req.GetTimeout(),
		AllowInsecure:     req.GetAllowInsecure(),
		NoFollowRedirects: req.GetNoFollowRedirects(),
		CAPEM:             req.GetCaPem(),
	})
	if err != nil {
		return &pb.HTTPDoReply{Error: err.Error()}, nil
	}
	return &pb.HTTPDoReply{Status: int32(resp.Status), Body: resp.Body, HeaderBlob: resp.HeaderBlob}, nil
}

// ResolveEndpoint resolves the check target's venue to a host-reachable addr for an in-venue
// TCP port via the host's resolveVerbEndpoint (the SAME machinery the in-process
// runnerCheckContext.ResolveEndpoint uses, R3). Any ssh -L forward it opens is tracked on the
// Runner and closed after the calling verb's Invoke. A resolution failure rides the reply
// error field (the RPC itself succeeds, like HTTPDoReply).
func (s *checkContextReverseServer) ResolveEndpoint(_ context.Context, req *pb.ResolveEndpointRequest) (*pb.ResolveEndpointReply, error) {
	if s.resolveEp == nil {
		return &pb.ResolveEndpointReply{Error: "endpoint resolution unavailable (no runner context)"}, nil
	}
	addr, err := s.resolveEp(int(req.GetPort()))
	if err != nil {
		return &pb.ResolveEndpointReply{Error: err.Error()}, nil
	}
	return &pb.ResolveEndpointReply{Addr: addr}, nil
}

// ResolveGraphicsEndpoint resolves a VM's <graphics type='<kind>'> listener to a dialable
// endpoint via the host's resolveVerbGraphics (the SAME machinery the in-process
// runnerCheckContext.ResolveGraphicsEndpoint uses, R3). Any ssh -L forward it opens is tracked
// on the Runner and closed after the calling verb's Invoke. A resolution failure rides the
// reply error field; Skip signals an N/A (no graphics device of that kind).
func (s *checkContextReverseServer) ResolveGraphicsEndpoint(_ context.Context, req *pb.ResolveGraphicsEndpointRequest) (*pb.ResolveGraphicsEndpointReply, error) {
	if s.resolveGfx == nil {
		return &pb.ResolveGraphicsEndpointReply{Error: "graphics endpoint resolution unavailable (no runner context)"}, nil
	}
	ge, err := s.resolveGfx(req.GetKind())
	if err != nil {
		return &pb.ResolveGraphicsEndpointReply{Error: err.Error()}, nil
	}
	return &pb.ResolveGraphicsEndpointReply{
		Addr: ge.Addr, Socket: ge.Socket, Password: ge.Password, Skip: ge.Skip, SkipMessage: ge.SkipMessage,
	}, nil
}

// ResolveClusterContext maps a k8s cluster-profile name to its kubeconfig context via the host's
// resolveClusterContext (the SAME project-loader leg the in-process runnerCheckContext uses, R3).
// An empty context (no matching profile) is a valid reply — the plugin falls back to the
// kubeconfig current-context.
func (s *checkContextReverseServer) ResolveClusterContext(_ context.Context, req *pb.ResolveClusterContextRequest) (*pb.ResolveClusterContextReply, error) {
	if s.resolveClusterCtx == nil {
		return &pb.ResolveClusterContextReply{Error: "cluster-context resolution unavailable (no runner context)"}, nil
	}
	ctx, err := s.resolveClusterCtx(req.GetCluster())
	if err != nil {
		return &pb.ResolveClusterContextReply{Error: err.Error()}, nil
	}
	return &pb.ResolveClusterContextReply{Context: ctx}, nil
}

// ResolveImageLabel reads one raw OCI label off the deployment image via the host's
// resolveImageLabel (the SAME leg the in-process runnerCheckContext uses, R3). Empty value
// (label absent / no live deployment) is a valid reply.
func (s *checkContextReverseServer) ResolveImageLabel(_ context.Context, req *pb.ResolveImageLabelRequest) (*pb.ResolveImageLabelReply, error) {
	if s.resolveImgLabel == nil {
		return &pb.ResolveImageLabelReply{Error: "image-label resolution unavailable (no runner context)"}, nil
	}
	v, err := s.resolveImgLabel(req.GetLabel())
	if err != nil {
		return &pb.ResolveImageLabelReply{Error: err.Error()}, nil
	}
	return &pb.ResolveImageLabelReply{Value: v}, nil
}

// AddBackground registers a host-side background PID with the active plan run for teardown
// reap. A no-op when the engine has no scenario context (addBg nil) or pid<=0.
func (s *checkContextReverseServer) AddBackground(_ context.Context, req *pb.AddBackgroundRequest) (*pb.Empty, error) {
	if s.addBg != nil && req.GetPid() > 0 {
		s.addBg(int(req.GetPid()))
	}
	return &pb.Empty{}, nil
}
