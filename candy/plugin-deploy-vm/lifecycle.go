package deployvm

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// lifecycle.go — the host-side VM venue lifecycle, externalized (M4b). The vm venue lifecycle is
// DEEPLY host-coupled (LoadUnified → spec.Vm, libvirt boot, the managed ssh-config, the guest
// readiness waits + EnsureCharlyInGuest, VmDeployState), so "it needs core" (CLAUDE.md doctrine):
// the logic stays host-side in charly's vmSubstrateLifecycle, and this plugin — the substrate
// INTERFACE — reaches it over the GENERIC "cli" host seam, forwarding each lifecycle Op to the
// hidden `charly __vm-lifecycle <op> <name>` dispatch (the vm analog of pod's HostBuild("overlay")).
// The Op keywords are the sdk.Op* strings verbatim, and the __vm-lifecycle command prints the exact
// reply JSON the grpcSubstrateLifecycle proxy expects, so a data Op just passes its stdout through.

// vmDataOps are the lifecycle Ops whose __vm-lifecycle dispatch PRINTS a reply the proxy decodes
// (PrepareVenueReply / PostTeardownReply / VenueDescriptor / {key} / StatusInfo). The rest are pure
// actions (charly vm start/stop/console/ssh/… + deployNestedPodsInGuest) that print nothing.
var vmDataOps = map[string]bool{
	sdk.OpPrepareVenue: true, sdk.OpPostTeardown: true, sdk.OpTeardownExecutor: true,
	sdk.OpArtifactKey: true, sdk.OpStatus: true,
}

func isLifecycleOp(op string) bool {
	switch op {
	case sdk.OpPrepareVenue, sdk.OpArtifactKey, sdk.OpPostApply, sdk.OpTeardownExecutor,
		sdk.OpPostTeardown, sdk.OpStart, sdk.OpStop, sdk.OpStatus, sdk.OpLogs, sdk.OpShell, sdk.OpRebuild:
		return true
	}
	return false
}

// invokeLifecycle forwards a vm substrate-lifecycle Op to `charly __vm-lifecycle <op> <name>` over
// the "cli" host seam.
func invokeLifecycle(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm %s: executor: %w", req.GetOp(), err)
	}
	var p struct {
		Name      string          `json:"name"`
		Opts      json.RawMessage `json:"opts"`
		KeepImage bool            `json:"keep_image"`
		Cmd       []string        `json:"cmd"`
	}
	_ = json.Unmarshal(req.GetParamsJson(), &p)

	op := req.GetOp()
	argv := []string{"__vm-lifecycle", op, p.Name}
	switch op {
	case sdk.OpPostTeardown:
		if p.KeepImage {
			argv = append(argv, "--keep-image")
		}
	case sdk.OpRebuild:
		var r struct {
			DryRun       bool `json:"DryRun"`
			RebuildImage bool `json:"RebuildImage"`
		}
		_ = json.Unmarshal(p.Opts, &r)
		if r.RebuildImage {
			argv = append(argv, "--rebuild-image")
		}
		if r.DryRun {
			argv = append(argv, "--dry-run")
		}
	case sdk.OpLogs:
		var l struct {
			Follow bool `json:"Follow"`
			Tail   int  `json:"Tail"`
		}
		_ = json.Unmarshal(p.Opts, &l)
		if l.Follow {
			argv = append(argv, "--follow")
		}
		if l.Tail > 0 {
			argv = append(argv, "--tail", strconv.Itoa(l.Tail))
		}
	case sdk.OpShell:
		for _, c := range p.Cmd {
			argv = append(argv, "--cmd", c)
		}
	}

	capture := vmDataOps[op]
	reqJSON, err := json.Marshal(spec.CliRequest{Argv: argv, Capture: capture})
	if err != nil {
		return nil, err
	}
	resJSON, err := exec.HostBuild(ctx, "cli", reqJSON)
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm %s: %w", op, err)
	}
	var r spec.CliReply
	if err := json.Unmarshal(resJSON, &r); err != nil {
		return nil, err
	}
	if r.Error != "" {
		return nil, fmt.Errorf("charly __vm-lifecycle %s: %s", op, r.Error)
	}
	if capture {
		// __vm-lifecycle printed the exact reply JSON the proxy decodes — pass it through.
		return &pb.InvokeReply{ResultJson: []byte(strings.TrimSpace(r.Stdout))}, nil
	}
	return &pb.InvokeReply{}, nil
}
