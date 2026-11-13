package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	pb "github.com/opencharly/spec/proto"
)

// plugin_executor_reverse_novenue_test.go — the gate on the VENUE-LESS reverse channel.
//
// A reverse server is stood up for two different kinds of dispatch and only one of them has a
// venue: a deploy/step/builder/check-verb Invoke carries a live executor, while a COMMAND-class
// Invoke carries none by construction — its handler needs the host REGISTRY (InvokeProvider to
// reach the workflow engine, HostBuild, DescribeProvider) and runs no shell anywhere. The
// venue-less shape was already an established, tested concept (hostInProcCtx has always built it,
// and the venue tests call `&executorReverseServer{}` explicitly), but the venue legs dereferenced
// the nil executor: a plugin asking a venue-less dispatch for a venue PANICKED inside the charly
// host process instead of receiving an error it could report.
//
// opencharly/charly#1006 makes that shape reachable from an out-of-process plugin for the first
// time (the command dispatch lane), so every venue leg now answers with errNoVenue BY NAME. This
// test walks every one of them.
func TestExecutorReverseServer_VenueLessChannelNamesTheMissingVenue(t *testing.T) {
	srv := &executorReverseServer{} // no exec — the venue-less command dispatch
	ctx := context.Background()

	// Each leg returns its OWN reply type; the contract is that the RPC SUCCEEDS and the failure
	// travels in the reply's error field (the runReply convention the whole reverse channel uses),
	// with Venue being the one leg that returns an error directly.
	for _, tc := range []struct {
		name string
		call func() (string, error)
	}{
		{"Venue", func() (string, error) {
			_, err := srv.Venue(ctx, &pb.Empty{})
			return "", err
		}},
		{"RunSystem", func() (string, error) {
			r, err := srv.RunSystem(ctx, &pb.RunRequest{Script: "true"})
			if err != nil {
				return "", err
			}
			return r.GetError(), nil
		}},
		{"RunUser", func() (string, error) {
			r, err := srv.RunUser(ctx, &pb.RunRequest{Script: "true"})
			if err != nil {
				return "", err
			}
			return r.GetError(), nil
		}},
		{"PutFile", func() (string, error) {
			r, err := srv.PutFile(ctx, &pb.PutFileRequest{Content: []byte("x"), Path: "/tmp/x"})
			if err != nil {
				return "", err
			}
			return r.GetError(), nil
		}},
		{"RunCapture", func() (string, error) {
			r, err := srv.RunCapture(ctx, &pb.RunRequest{Script: "true"})
			if err != nil {
				return "", err
			}
			return r.GetError(), nil
		}},
		{"RunInteractive", func() (string, error) {
			r, err := srv.RunInteractive(ctx, &pb.RunRequest{Script: "true"})
			if err != nil {
				return "", err
			}
			return r.GetError(), nil
		}},
		{"RunStream", func() (string, error) {
			r, err := srv.RunStream(ctx, &pb.RunRequest{Script: "true"})
			if err != nil {
				return "", err
			}
			return r.GetError(), nil
		}},
		{"GetFile", func() (string, error) {
			r, err := srv.GetFile(ctx, &pb.GetFileRequest{Path: "/tmp/x"})
			if err != nil {
				return "", err
			}
			return r.GetError(), nil
		}},
		{"RunHostStep", func() (string, error) {
			r, err := srv.RunHostStep(ctx, &pb.HostStepRequest{StepJson: []byte(`{}`)})
			if err != nil {
				return "", err
			}
			return r.GetError(), nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A panic here is the defect this gate exists for: it would take the charly host
			// process down on a plugin's request.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s PANICKED on a venue-less channel (%v) — it must return the NAMED venue-less error instead, or an out-of-process command plugin can kill the host with a nil dereference", tc.name, r)
				}
			}()
			msg, err := tc.call()
			if err != nil {
				msg = err.Error()
			}
			if !strings.Contains(msg, errNoVenue.Error()) {
				t.Fatalf("%s on a venue-less channel reported %q, want the NAMED venue-less error (%q)", tc.name, msg, errNoVenue.Error())
			}
		})
	}
}

// TestExecutorReverseServer_VenueLessStillServesTheRegistryLegs pins the OTHER half: the reason a
// venue-less channel exists at all is that the REGISTRY legs need no venue. If a future change made
// the whole channel refuse to serve, the command lane would break again in the opposite direction —
// a dispatch that cannot reach InvokeProvider even with the broker attached.
func TestExecutorReverseServer_VenueLessStillServesTheRegistryLegs(t *testing.T) {
	srv := &executorReverseServer{}
	if _, err := srv.venue(); !errors.Is(err, errNoVenue) {
		t.Fatalf("venue() on a venue-less server = %v, want errNoVenue", err)
	}
	// DescribeProvider answers from the provider registry, so a venue-less channel serves it: that
	// is a peer asking "what can this host reach", which is exactly what the command lane needs.
	if _, err := srv.DescribeProvider(context.Background(), &pb.DescribeProviderRequest{Class: "workflow", Word: "zzabsent"}); err != nil {
		t.Fatalf("DescribeProvider on a venue-less channel returned a transport error %v — the registry legs must remain served", err)
	}
}
