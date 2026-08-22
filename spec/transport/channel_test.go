package transport

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	pb "github.com/opencharly/spec/proto"
)

func TestSequenceGate(t *testing.T) {
	gate := NewSequenceGate(7)
	if err := gate.Accept(&pb.ChannelFrame{Sequence: 7}); err != nil {
		t.Fatal(err)
	}
	if err := gate.Accept(&pb.ChannelFrame{Sequence: 9}); err == nil {
		t.Fatal("sequence gap accepted")
	}
	if got := gate.Expected(); got != 8 {
		t.Fatalf("expected sequence = %d, want 8", got)
	}
	if err := gate.Accept(&pb.ChannelFrame{Sequence: 9, Kind: ChannelResync}); err != nil {
		t.Fatal(err)
	}
	if got := gate.Expected(); got != 10 {
		t.Fatalf("post-resync sequence = %d, want 10", got)
	}
}

type relayUpstreamFixture struct {
	received bool
	sent     []*pb.ChannelFrame
}

func (f *relayUpstreamFixture) Context() context.Context { return context.Background() }
func (f *relayUpstreamFixture) Send(frame *pb.ChannelFrame) error {
	f.sent = append(f.sent, frame)
	return nil
}
func (f *relayUpstreamFixture) Recv() (*pb.ChannelFrame, error) {
	if f.received {
		return nil, io.EOF
	}
	f.received = true
	return &pb.ChannelFrame{Kind: ChannelStdin, Sequence: 1, Data: []byte("input")}, nil
}

type relayDownstreamFixture struct {
	closeOnce sync.Once
	closed    chan struct{}
	received  bool
	sent      []*pb.ChannelFrame
}

func (f *relayDownstreamFixture) Context() context.Context { return context.Background() }
func (f *relayDownstreamFixture) Send(frame *pb.ChannelFrame) error {
	f.sent = append(f.sent, frame)
	return nil
}
func (f *relayDownstreamFixture) Recv() (*pb.ChannelFrame, error) {
	<-f.closed
	if f.received {
		return nil, io.EOF
	}
	f.received = true
	return &pb.ChannelFrame{Kind: ChannelStatus, Sequence: 7, Name: "drained"}, nil
}
func (f *relayDownstreamFixture) CloseSend() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func TestRelayChannelHalfClosesInputAndDrainsProviderEvidence(t *testing.T) {
	upstream := &relayUpstreamFixture{}
	downstream := &relayDownstreamFixture{closed: make(chan struct{})}
	if err := RelayChannel(upstream, downstream); err != nil {
		t.Fatal(err)
	}
	if len(downstream.sent) != 1 || string(downstream.sent[0].GetData()) != "input" {
		t.Fatalf("downstream input = %#v", downstream.sent)
	}
	if len(upstream.sent) != 1 || upstream.sent[0].GetName() != "drained" {
		t.Fatalf("provider evidence was not drained after input EOF: %#v", upstream.sent)
	}
}

type providerFirstUpstreamFixture struct {
	release <-chan struct{}
	sent    []*pb.ChannelFrame
}

func (f *providerFirstUpstreamFixture) Context() context.Context { return context.Background() }
func (f *providerFirstUpstreamFixture) Send(frame *pb.ChannelFrame) error {
	f.sent = append(f.sent, frame)
	return nil
}
func (f *providerFirstUpstreamFixture) Recv() (*pb.ChannelFrame, error) {
	<-f.release
	return nil, io.EOF
}

type providerFirstDownstreamFixture struct{ received bool }

func (f *providerFirstDownstreamFixture) Context() context.Context    { return context.Background() }
func (f *providerFirstDownstreamFixture) Send(*pb.ChannelFrame) error { return nil }
func (f *providerFirstDownstreamFixture) Recv() (*pb.ChannelFrame, error) {
	if f.received {
		return nil, io.EOF
	}
	f.received = true
	return &pb.ChannelFrame{Kind: ChannelExit, Sequence: 1}, nil
}
func (f *providerFirstDownstreamFixture) CloseSend() error { return io.EOF }

func TestRelayChannelProviderCompletionIsNotReportedAsInputEOF(t *testing.T) {
	release := make(chan struct{})
	upstream := &providerFirstUpstreamFixture{release: release}
	downstream := &providerFirstDownstreamFixture{}
	if err := RelayChannel(upstream, downstream); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if len(upstream.sent) != 1 || upstream.sent[0].GetKind() != ChannelExit {
		t.Fatalf("provider completion evidence = %#v", upstream.sent)
	}
}

func TestReplayBufferAcknowledgementAndResync(t *testing.T) {
	b := NewReplayBuffer(2, 1024)
	if err := b.Add(&pb.ChannelFrame{Sequence: 1, Kind: ChannelStdout, Data: []byte("one")}); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(&pb.ChannelFrame{Sequence: 2, Kind: ChannelStdout, Data: []byte("two")}); err != nil {
		t.Fatal(err)
	}
	err := b.Add(&pb.ChannelFrame{Sequence: 3})
	if err == nil {
		t.Fatal("unacknowledged eviction was silent")
	}
	// The diagnostic must name the unacknowledged OLDEST frame (1), not the
	// incoming frame (3) that tripped the bound.
	if got, want := err.Error(), "unacknowledged sequence 1"; !strings.Contains(got, want) {
		t.Fatalf("eviction diagnostic = %q, want it to name %q", got, want)
	}
	b.Acknowledge(1)
	if err := b.Add(&pb.ChannelFrame{Sequence: 3, Kind: ChannelExit}); err != nil {
		t.Fatal(err)
	}
	got, err := b.ReplayFrom(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].GetSequence() != 2 || got[1].GetSequence() != 3 {
		t.Fatalf("replay = %#v", got)
	}
	if _, err := b.ReplayFrom(1); err == nil {
		t.Fatal("evicted replay did not request resynchronization")
	}
}
