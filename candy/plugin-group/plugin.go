// Package groupkind is the importable form of charly's `group` STRUCTURAL KIND — a TARGETLESS deploy
// group (resource members brought up ALONGSIDE on the shared network, no own workload; the former
// targetless `bundle:`). A structural KIND provider dispatches via the pb Invoke(OpLoad) envelope:
// decode the group's KIND-SPECIFIC scalar config (disposable/lifecycle/description/…) from op.Params
// into a spec.Deploy, ATTACH the AUTHORED members the host pre-decoded + threaded via op.Env
// (spec.StructuralKindLoadEnv — F5 authored-member input-threading), force Target="" (a group is
// targetless), and return the COMPLETE spec.Deploy — which runPluginKind folds into uf.Bundle,
// BYTE-EQUIVALENT to the former builtin groupKind (buildBundleNodeInto). Usable COMPILED-IN
// (NewProvider()/NewMeta() via plugins_generated.go) OR served OUT-OF-PROCESS by the cmd/serve shim.
//
// PLACEMENT — COMPILED-IN (listed in the embedded charly/charly.yml compiled_plugins:), NOT external.
// group is a CORE deploy primitive that must ALWAYS resolve: every box/submodule authoring a `group:`
// node (check-k8s-deploy, box/fedora, box/cachyos) relies on it without discovering this candy from
// the superproject, exactly like the tier-1 kinds (distro/target/agent/…) which are all compiled-in.
// (The out-of-process-only reference is candy/plugin-example-structkind — a disposable witness.)
//
// Relocated out of charly's module (formerly charly/plugin_group.go's groupKind + cue_kind_group.go).
package groupkind

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.182.0440"

// NewProvider returns the kind provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta ships the STRUCTURAL group kind capability (Class "kind", word "group", Structural:true —
// the F5 flag that makes the host pre-decode + thread the authored members via op.Env and fold the
// reply into uf.Bundle) + its self-contained #GroupInput schema (via sdk.NewMeta → BuildCapabilities).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "kind", Word: "group", InputDef: "#GroupInput", Structural: true}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpLoad: decode the group's TARGETLESS scalar config from op.Params into a spec.Deploy
// (the host validated it against #GroupInput first), attach the host-threaded AUTHORED members from
// op.Env, force Target="" (a group has no own workload — its members are PEERS, exactly as the former
// builtin groupKind via bundleTargetForDisc("group")=""), and return the complete spec.Deploy.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpLoad {
		return nil, fmt.Errorf("group kind: unsupported op %q (only %q)", req.GetOp(), sdk.OpLoad)
	}
	var dep spec.Deploy
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &dep); err != nil {
			return nil, fmt.Errorf("group kind: decode group config: %w", err)
		}
	}
	// F5 authored-member input-threading: attach the members the host pre-decoded via the SAME core
	// buildBundleNode recursion the former builtin path used, and threaded here in op.Env.
	var env spec.StructuralKindLoadEnv
	if len(req.GetEnvJson()) > 0 {
		if err := json.Unmarshal(req.GetEnvJson(), &env); err != nil {
			return nil, fmt.Errorf("group kind: decode member env: %w", err)
		}
	}
	// A group is TARGETLESS: no own workload, members are PEERS (Members). Force these so an authored
	// stray target/member can never leak (they are loader-derived; #GroupInput never admits them).
	dep.Target = ""
	dep.Members = env.Members
	dep.Children = nil
	out, err := json.Marshal(dep)
	if err != nil {
		return nil, fmt.Errorf("group kind: marshal deploy: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}
