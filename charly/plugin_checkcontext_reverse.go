package main

import (
	"context"
	"net/http"

	"github.com/opencharly/charly/charly/plugin/kit"
	pb "github.com/opencharly/charly/charly/plugin/proto"
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
	httpBase *http.Client  // the engine's base HTTP client (default timeout); per-request policy applied per call
	addBg    func(pid int) // r.Scenario.AddBackground, nil when there is no scenario context
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

// AddBackground registers a host-side background PID with the active plan run for teardown
// reap. A no-op when the engine has no scenario context (addBg nil) or pid<=0.
func (s *checkContextReverseServer) AddBackground(_ context.Context, req *pb.AddBackgroundRequest) (*pb.Empty, error) {
	if s.addBg != nil && req.GetPid() > 0 {
		s.addBg(int(req.GetPid()))
	}
	return &pb.Empty{}, nil
}
