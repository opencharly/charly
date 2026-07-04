// Command plugin-example-structkind is a reference OUT-OF-TREE charly class:kind plugin (F5):
// it serves the `examplestructkind` STRUCTURAL entity KIND over go-plugin gRPC. Unlike the FLAT
// kind (candy/plugin-example-kind, F4 — body → opaque uf.PluginKinds), a STRUCTURAL kind's
// OpLoad returns a spec.Deploy (BundleNode) MEMBER TREE that the host folds into uf.Bundle — the
// SAME map a builtin structural kind (pod/group/candy) populates in-proc — so the entity
// participates in deploy/check exactly like a builtin. It declares Structural:true in Describe.
//
// F5 authored-member INPUT-threading: the AUTHORED resource-member children of the kind node are
// pre-decoded HOST-SIDE (via the core buildBundleNode recursion — the one member-decode source of
// truth) and threaded to this plugin's OpLoad via op.Env (spec.StructuralKindLoadEnv); the plugin
// decodes only its kind-specific scalar body from op.Params (closed against #ExamplestructkindInput)
// and ATTACHES the host-threaded members to its reply — so the reconstructed uf.Bundle carries the
// AUTHORED member tree (peers, nested children, cross-member ${HOST:…} checks), identical to a
// builtin group. (An earlier version SYNTHESIZED a single member from `marker`; that never proved
// authored-member reconstruction — the whole point of F5 and the group/substrate externalizations.)
//
// NOT in compiled_plugins (out-of-process only): the witness that a plugin the loader was not
// built with can reconstruct an AUTHORED uf.Bundle member tree over the wire. The structural
// companion of the flat kind plugin; this is the channel the group/pod/vm/k8s/local/android/candy
// externalizations reuse.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.182.0440"

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// structkindBody is the authored `examplestructkind:` entity SCALAR body decoded host→plugin via
// op.Params (validated against the CLOSED #ExamplestructkindInput). It carries ONLY kind-specific
// scalars + the deploy-config passthrough (disposable/lifecycle/description) — the authored MEMBER
// children are NOT here (op.Params is closed); they arrive host-pre-decoded via op.Env.
type structkindBody struct {
	Marker      string `json:"marker,omitempty"`
	Disposable  *bool  `json:"disposable,omitempty"`
	Lifecycle   string `json:"lifecycle,omitempty"`
	Description string `json:"description,omitempty"`
}

// Invoke handles OpLoad: decode the kind-specific scalar body from op.Params, ATTACH the authored
// member tree the host pre-decoded + threaded via op.Env (F5 authored-member input-threading), and
// return a TARGETLESS spec.Deploy whose Members are those AUTHORED members — proving the authored
// member subtree round-trips through the plugin into the tree the host folds into uf.Bundle,
// identical to a builtin group's in-proc decode. (The former version SYNTHESIZED a single member
// from `marker`; it never exercised authored-member reconstruction — the whole point of F5.)
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpLoad {
		return nil, fmt.Errorf("examplestructkind: unsupported op %q (only %q)", req.GetOp(), sdk.OpLoad)
	}
	var in structkindBody
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("examplestructkind: decode entity: %w", err)
		}
	}
	// F5 authored-member input-threading: the host pre-decoded the authored resource-member
	// children (via the core buildBundleNode recursion — the SAME source the builtin path uses)
	// and threaded them in op.Env. Attach them to the reply so runPluginKind folds a COMPLETE
	// Bundle (with the authored members) into uf.Bundle.
	var env spec.StructuralKindLoadEnv
	if len(req.GetEnvJson()) > 0 {
		if err := json.Unmarshal(req.GetEnvJson(), &env); err != nil {
			return nil, fmt.Errorf("examplestructkind: decode member env: %w", err)
		}
	}
	desc := in.Description
	if desc == "" && in.Marker != "" {
		desc = "examplestructkind:" + in.Marker
	}
	// A TARGETLESS structural entity (Target "") — its authored members are PEERS (Members),
	// exactly like a builtin group. The kind-specific + deploy-config scalars ride op.Params;
	// the authored member tree rides op.Env (host-pre-decoded, input-threaded).
	dep := spec.Deploy{
		Description: desc,
		Lifecycle:   in.Lifecycle,
		Disposable:  in.Disposable,
		Members:     env.Members,
	}
	out, err := json.Marshal(dep)
	if err != nil {
		return nil, fmt.Errorf("examplestructkind: marshal deploy: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the STRUCTURAL kind capability (Class "kind", word "examplestructkind",
// Structural:true) — the F5 flag that makes the host fold its OpLoad reply into uf.Bundle.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "kind", Word: "examplestructkind", InputDef: "#ExamplestructkindInput", Structural: true}},
		schemaFS, "schema")
}
