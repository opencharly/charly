package tmux

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// provider serves the placement-neutral terminal:tmux and agent-runtime:tmux
// capabilities through the generated bidirectional Provider.Channel protocol.

type provider struct{ pb.UnimplementedProviderServer }

// Invoke is intentionally unsupported: terminal operations are streaming and
// therefore require Provider.Channel.
func (provider) Invoke(context.Context, *pb.InvokeRequest) (*pb.InvokeReply, error) {
	return nil, fmt.Errorf("plugin-tmux: terminal operations require Provider.Channel")
}

func (provider) OpenChannel(open *pb.ChannelFrame, stream sdk.ProviderChannel) error {
	if open.GetClass() == "terminal" {
		return openTmuxChannel(open, stream, "")
	}
	if open.GetClass() != "agent-runtime" {
		return fmt.Errorf("plugin-tmux: unsupported channel %s:%s", open.GetClass(), open.GetReserved())
	}
	var request spec.AgentRunRequest
	if err := json.Unmarshal(open.GetPayloadJson(), &request); err != nil {
		return fmt.Errorf("plugin-tmux: decode AgentRunRequest: %w", err)
	}
	if err := sdk.ValidateGenerated("#AgentRunRequest", request); err != nil {
		return fmt.Errorf("plugin-tmux: %w", err)
	}
	rawProfile, ok := request.Params["terminal_profile"]
	if !ok {
		return fmt.Errorf("plugin-tmux: agent runtime requires CUE AgentSession.terminal_profile")
	}
	data, err := json.Marshal(rawProfile)
	if err != nil {
		return err
	}
	var profile spec.TerminalProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return fmt.Errorf("plugin-tmux: decode TerminalProfile: %w", err)
	}
	if err := sdk.ValidateGenerated("#TerminalProfile", profile); err != nil {
		return fmt.Errorf("plugin-tmux: %w", err)
	}
	open.PayloadJson, err = json.Marshal(profile)
	if err != nil {
		return err
	}
	return openTmuxChannel(open, stream, request.Prompt)
}

func (p provider) Channel(stream pb.Provider_ChannelServer) error {
	open, err := sdk.ReceiveChannelOpen(stream)
	if err != nil {
		return err
	}
	return p.OpenChannel(open, stream)
}
