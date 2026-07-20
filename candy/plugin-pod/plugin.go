// Package pod is the charly plugin housing the `charly start`/`stop`/`restart`/`config`/`shell`/
// `service`/`logs`/`remove`/`cp`/`volume` pod-lifecycle CLI (the DEPLOY-wave CLI-struct port). It is
// a dual-placement COMMAND plugin (F8): the SAME NewProvider()/NewMeta()/CliMain compile INTO
// charly in-process when listed in compiled_plugins (the canonical placement), or cmd/serve serves
// them OUT-OF-PROCESS when they are not.
//
// It is the CLI sibling of candy/plugin-deploy-pod, NOT the same candy — mirroring the
// candy/plugin-vm (command:vm, compiled-in) / candy/plugin-deploy-vm (deploy:vm, out-of-process)
// split: the pod deploy SUBSTRATE (deploy:pod) stays out-of-process in candy/plugin-deploy-pod,
// untouched by this candy's placement.
//
// Each command word is INDEPENDENT (no shared parent — unlike `charly bundle …`'s single grouped
// word): some (restart) are pure sdk/kit + sdk/deploykit logic with NO host coupling; others
// (start/stop/logs/shell/…) need the provider REGISTRY (ResolveTarget, the plugin loader) — a
// kernel M-mechanism a plugin cannot hold — and reach it over HostBuild seams that reconstruct the
// original core orchestration struct and run its Run() logic VERBATIM (mirroring
// candy/plugin-bundle's deploy-add/deploy-del host seams). See host_seams.go for the exact split.
//
// COMPILED-IN, it dispatches IN-PROC via Invoke(OpRun), so the handlers run in charly's OWN process
// and inherit charly's real stdio/TTY natively (`charly shell` stays interactive).
package pod

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// calver is the candy's identity CalVer (advertised over Describe).
const calver = "2026.201.0000"

// podCommandWords is the set of independent top-level command words this plugin serves. Grows
// as each command's port lands (config/shell/service/logs/remove/cp/volume still pending) — a
// word is added here ONLY in the same change that deletes its old plugin_command_*.go shim
// (never both registered at once, which panics the startup bijection-style duplicate-provider
// guard).
var podCommandWords = []string{"start", "stop", "restart", "logs", "remove", "shell"}

// NewProvider returns the pod command provider for in-proc registration (compiled-in) or
// out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:start/stop/restart/config/shell/service/logs/remove/cp/volume via
// sdk.NewMeta → BuildCapabilities so the COMPILED-IN path registers each as a command provider (the
// host builds its dynamic Kong grammar + dispatches Invoke(OpRun)). A command's args are
// pass-through CLI tokens, not a structured plugin_input, so the capabilities carry no InputDef and
// the plugin ships no schema.
func NewMeta() pb.PluginMetaServer {
	caps := make([]sdk.ProvidedCapability, 0, len(podCommandWords))
	for _, w := range podCommandWords {
		caps = append(caps, sdk.ProvidedCapability{Class: "command", Word: w})
	}
	return sdk.NewMeta(calver, caps, nil)
}

// CliMain is the OUT-OF-PROCESS command entry — unreachable in the canonical compiled-in placement.
// The registry-bound handlers (start/stop/logs/shell/service/config/remove/cp/volume) reach the
// host reverse channel, which is unavailable out-of-process, so this errors (like
// candy/plugin-bundle's / candy/plugin-authoring's CliMain).
func CliMain(_ []string) int {
	fmt.Println("charly: pod lifecycle commands require compiled-in placement (the host reverse channel is unavailable out-of-process)")
	return 1
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke serves the pod commands' Invoke(OpRun): recover the reverse-channel executor, decode the
// pass-through args, dispatch by the reserved command word. In-proc dispatch runs in charly's own
// process (native stdio/TTY).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("pod: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("pod command: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	word := req.GetReserved()
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if uerr := json.Unmarshal(req.GetParamsJson(), &in); uerr != nil {
			return nil, fmt.Errorf("pod %s: decode args: %w", word, uerr)
		}
	}
	if rerr := dispatchPodCommand(word, in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}
