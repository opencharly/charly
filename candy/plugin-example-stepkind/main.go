// Command plugin-example-stepkind is a reference OUT-OF-TREE charly class:step plugin that
// proves the F3 EXTERNAL STEP-KIND contract in BOTH legs: a plugin CONTRIBUTES a first-class
// install-step KIND ("external:examplestepkind") whose Scope/Venue/Gate it DECLARES in its
// Describe StepContract, carried OPAQUELY through the IR (InstallStepView.Payload).
//
//   - DEPLOY leg (OpExecute): the host's OPEN DEFAULT ARM (no compiled-in case) dispatches the
//     step's OpExecute over the E3b reverse channel — where it writes a venue marker and returns
//     a teardown ReverseOp the host records + replays (record-and-replay).
//   - BUILD leg (OpEmit, F-STEP-EMIT): the plugin DECLARES Emits=true and answers OpEmit with a
//     spec.EmitReply whose Fragment is a Containerfile RUN. When the step is composed into a POD
//     overlay (add_candy), the host's OCITarget open external-step arm Invokes this OpEmit and
//     splices the fragment verbatim — baking a persistent marker file into the overlay image.
//     This is a PURE step: the fragment is self-contained (no host build-engine callback), so it
//     needs no HostBuild round-trip. It proves the OCITarget external-step build-emit arm C1
//     needs to externalize a step kind whose EmitOCI produces a Containerfile fragment.
//
// charly host-builds it + serves it OUT-OF-PROCESS over go-plugin gRPC (LocalTransport), the
// same path the deploy/verb example plugins ride.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/opencharly/charly/charly/plugin/kit"
	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.182.0900"

// markerDir is the disposable venue scratch dir the DEPLOY leg (OpExecute) writes into — under
// /tmp, user-owned, removed by the recorded teardown op (zero operator side-effect).
const markerDir = "/tmp/charly-examplestepkind"
const markerPath = markerDir + "/marker"

// buildMarkerPath is where the BUILD leg (OpEmit) bakes its marker file — a PERSISTENT image
// path (/etc, not /tmp) so the pod-overlay bed can assert it INSIDE the running container.
const buildMarkerPath = "/etc/examplestepkind-build-baked"

// opEmit / opExecute mirror charly's op selectors (package main's OpEmit = "emit", OpExecute =
// "execute"). An external plugin can't import those constants; the host sends opEmit on the
// BUILD-context Invoke (pod-overlay OCITarget) and opExecute on the DEPLOY-context Invoke.
const (
	opEmit    = "emit"
	opExecute = "execute"
)

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// stepInput is the OPAQUE Payload (the external step's plugin_input) decoded host→plugin — the
// SAME shape for BOTH legs (build op.Params and deploy op.Params).
type stepInput struct {
	Marker string `json:"marker"`
}

// Invoke serves BOTH legs of the external step kind, dispatched by the op selector:
//   - opEmit (build): return a spec.EmitReply whose Fragment is a Containerfile RUN baking the
//     marker (from the opaque payload, proving it round-trips through OpEmit too) into a
//     persistent image path. NO executor — a pure build-context emit.
//   - opExecute (deploy): dial the host executor over the reverse channel, write the venue
//     marker, and return a teardown ReverseOp (record-and-replay).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in stepInput
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-example-stepkind: decode payload: %w", err)
		}
	}
	switch req.GetOp() {
	case opEmit:
		marker := in.Marker
		if marker == "" {
			marker = "EXAMPLE-STEPKIND-BUILD-BAKED"
		}
		fragment := fmt.Sprintf("RUN mkdir -p /etc && printf '%%s\\n' %s > %s\n", kit.ShellQuote(marker), buildMarkerPath)
		j, err := json.Marshal(spec.EmitReply{Fragment: fragment})
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: j}, nil
	case opExecute:
		exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
		if err != nil {
			return nil, err
		}
		if in.Marker == "" {
			in.Marker = "EXAMPLE-STEPKIND-OK"
		}
		script := fmt.Sprintf("mkdir -p %s && printf '%%s\\n' %s > %s", markerDir, kit.ShellQuote(in.Marker), markerPath)
		if err := exec.RunUser(ctx, script, nil); err != nil {
			return nil, fmt.Errorf("plugin-example-stepkind: write marker: %w", err)
		}
		reverseOps := []spec.ReverseOp{sdk.PluginScriptReverseOp(spec.ScopeUser, "rm -rf "+markerDir)}
		return sdk.BuildDeployReply(reverseOps, "plugin-example-stepkind", calver)
	default:
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	}
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the step:examplestepkind capability WITH its DECLARED StepContract
// (Scope user, Venue host-native (0), no gate, Emits=true) — the F3 plugin-declared install-step
// contract the host carries through the IR and applies via the open default arm. Emits=true (the
// F-STEP-EMIT flag) tells the pod-overlay OCITarget the step bakes a build-context fragment.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{
			Class:        "step",
			Word:         "examplestepkind",
			InputDef:     "#ExamplestepkindInput",
			StepContract: &sdk.StepContract{Scope: "user", Venue: 0, Gate: "", Emits: true},
		}},
		schemaFS, "schema")
}
