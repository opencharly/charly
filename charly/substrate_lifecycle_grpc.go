package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// grpcSubstrateLifecycle implements the in-core substrateLifecycle interface by Invoking an
// OUT-OF-PROCESS substrate plugin's lifecycle Ops (F6) — the host→plugin counterpart of the
// plugin→host ExecutorService. PrepareVenue / TeardownExecutor re-materialize the plugin's returned
// VenueDescriptor into a real host-side DeployExecutor (the LIVE executor never crosses the wire —
// the host rebuilds it independently, then serves THAT over the existing ExecutorService to the
// deploy-walk plugin); the rest carry name/node/opts in and error/StatusInfo out. Registered via
// registerSubstrateLifecycle at plugin-load, so substrateLifecycleFor returns it transparently and
// externalDeployTarget (which calls ONLY through the interface) needs no change (R3).
type grpcSubstrateLifecycle struct {
	prov *grpcProvider
}

// venueFromDescriptor re-materializes a VenueDescriptor into a real host-side DeployExecutor — the
// decouple point that lets a substrate lifecycle plugin run out-of-process: it returns a serializable
// venue description, the host owns the live executor.
func venueFromDescriptor(d spec.VenueDescriptor) (DeployExecutor, error) {
	switch d.Kind {
	case "":
		return nil, nil // no venue (e.g. TeardownExecutor declining → caller keeps its executor)
	case "shell":
		return ShellExecutor{}, nil
	case "ssh":
		return &SSHExecutor{User: d.User, Host: d.Host, Port: d.Port, Args: d.Args, ConnectTimeout: d.ConnectTimeout}, nil
	default:
		return nil, fmt.Errorf("substrate lifecycle: unknown venue descriptor kind %q", d.Kind)
	}
}

// marshalDeployOpParams marshals the common (name, dir, node, extra) args for a host→plugin deploy
// Op (the lifecycle Ops + the preresolver, R3). node is marshalled as the canonical BundleNode JSON
// so the plugin sees the SAME node the host decoded.
func marshalDeployOpParams(name, dir string, node *BundleNode, extra map[string]any) (json.RawMessage, error) {
	params := map[string]any{"name": name}
	if dir != "" {
		params["dir"] = dir
	}
	if node != nil {
		nj, err := marshalJSON(node)
		if err != nil {
			return nil, err
		}
		params["node"] = nj
	}
	for k, v := range extra {
		params[k] = v
	}
	return marshalJSON(params)
}

// lifecycleInvoke marshals the common args and Invokes the lifecycle op.
func (l grpcSubstrateLifecycle) lifecycleInvoke(ctx context.Context, op, name, dir string, node *BundleNode, extra map[string]any) (*Result, error) {
	pj, err := marshalDeployOpParams(name, dir, node, extra)
	if err != nil {
		return nil, err
	}
	return l.prov.Invoke(ctx, &Operation{Reserved: l.prov.word, Op: op, Params: pj})
}

func (l grpcSubstrateLifecycle) PrepareVenue(ctx context.Context, name, dir string, node *BundleNode, plans []*InstallPlan, opts EmitOpts) (DeployExecutor, error) {
	extra := map[string]any{"opts": opts}
	if len(plans) > 0 {
		extra["plans"] = plans
	}
	res, err := l.lifecycleInvoke(ctx, sdk.OpPrepareVenue, name, dir, node, extra)
	if err != nil {
		return nil, err
	}
	var d spec.VenueDescriptor
	if err := json.Unmarshal(res.JSON, &d); err != nil {
		return nil, fmt.Errorf("substrate %q prepare-venue: decode venue descriptor: %w", l.prov.word, err)
	}
	return venueFromDescriptor(d)
}

func (l grpcSubstrateLifecycle) ArtifactKey(name string, node *BundleNode) string {
	res, err := l.lifecycleInvoke(context.Background(), sdk.OpArtifactKey, name, "", node, nil)
	if err != nil || len(res.JSON) == 0 {
		return "" // best-effort: caller keys by the deploy name on empty
	}
	var out struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(res.JSON, &out) != nil {
		return ""
	}
	return out.Key
}

func (l grpcSubstrateLifecycle) PostApply(ctx context.Context, name, dir string, node *BundleNode, exec DeployExecutor, opts EmitOpts) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpPostApply, name, dir, node, map[string]any{"opts": opts})
	return err
}

func (l grpcSubstrateLifecycle) TeardownExecutor(name string, node *BundleNode) (DeployExecutor, error) {
	res, err := l.lifecycleInvoke(context.Background(), sdk.OpTeardownExecutor, name, "", node, nil)
	if err != nil {
		return nil, err
	}
	var d spec.VenueDescriptor
	if len(res.JSON) == 0 {
		return nil, nil // caller keeps its ResolveTarget-selected executor
	}
	if err := json.Unmarshal(res.JSON, &d); err != nil {
		return nil, fmt.Errorf("substrate %q teardown-executor: decode venue descriptor: %w", l.prov.word, err)
	}
	return venueFromDescriptor(d)
}

func (l grpcSubstrateLifecycle) PostTeardown(name string, node *BundleNode, keepImage bool) error {
	_, err := l.lifecycleInvoke(context.Background(), sdk.OpPostTeardown, name, "", node, map[string]any{"keep_image": keepImage})
	return err
}

func (l grpcSubstrateLifecycle) Start(ctx context.Context, name string, node *BundleNode) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpStart, name, "", node, nil)
	return err
}

func (l grpcSubstrateLifecycle) Stop(ctx context.Context, name string, node *BundleNode) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpStop, name, "", node, nil)
	return err
}

func (l grpcSubstrateLifecycle) Status(ctx context.Context, name string, node *BundleNode) (StatusInfo, error) {
	res, err := l.lifecycleInvoke(ctx, sdk.OpStatus, name, "", node, nil)
	if err != nil {
		return StatusInfo{}, err
	}
	var si StatusInfo
	if len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &si); err != nil {
			return StatusInfo{}, fmt.Errorf("substrate %q status: decode: %w", l.prov.word, err)
		}
	}
	return si, nil
}

func (l grpcSubstrateLifecycle) Logs(ctx context.Context, name string, node *BundleNode, opts LogsOpts) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpLogs, name, "", node, map[string]any{"opts": opts})
	return err
}

func (l grpcSubstrateLifecycle) Shell(ctx context.Context, name string, node *BundleNode, cmd []string) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpShell, name, "", node, map[string]any{"cmd": cmd})
	return err
}

func (l grpcSubstrateLifecycle) Rebuild(ctx context.Context, name string, node *BundleNode, opts RebuildOpts) error {
	_, err := l.lifecycleInvoke(ctx, sdk.OpRebuild, name, "", node, map[string]any{"opts": opts})
	return err
}
