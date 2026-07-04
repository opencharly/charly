// Command plugin-example-builder is a reference OUT-OF-TREE charly plugin that proves
// the BUILD-TIME plugin-execution BUILDER leg: `charly box build`/`generate` connects it
// out-of-process and Invokes its OpResolve during image generation, then splices the
// returned BuilderResolveReply — the multi-stage block (Stage) pre-main-FROM and the
// COPY --from artifacts (CopyArtifacts) post-main-FROM — into the
// .build/<image>/Containerfile. A candy SELECTS it via `external_builder: examplebuilder`;
// the spliced stage bakes a marker file the runtime check inspects. The BUILDER-leg
// counterpart of the verb/step candy/plugin-example-step.
//
// The operator-authorized build-time plugin-execution MECHANISM lives in charly (the
// generate.go NewGenerator connect seam + generate.go emitExternalBuilderStages /
// emitExternalBuilderArtifacts); this module is only the reference PAYLOAD that mechanism
// builds + executes.
package main

import (
	"context"
	"embed"
	"encoding/json"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// opResolve mirrors charly's build-time builder op selector (package main's OpResolve =
// "resolve"). An external plugin can't import that constant, so it is named here; the
// host sends it on the BUILDER-leg Invoke (verb/step ops use OpEmit, deploy/check use
// other selectors).
const opResolve = "resolve"

// Invoke handles the build-time OpResolve call: it returns a spec.BuilderResolveReply
// whose Stage is a multi-stage `FROM …  AS examplebuilder-stage` block the host splices
// pre-main-FROM, and whose CopyArtifacts pull the built marker into the final image
// (post-main-FROM) — so /opt/examplebuilder-artifact is baked into the image (proof the
// plugin executed at build). The host marshals the requesting candy name as op.Params
// and a spec.BuildEnv as op.Env — a real plugin tailors its stage per spec.BuildEnv.Distros;
// this example emits a static, deterministic stage. Any non-OpResolve op returns a benign
// empty result (this plugin contributes nothing at deploy/check time).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != opResolve {
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	}
	j, err := json.Marshal(spec.BuilderResolveReply{
		Stage:         "FROM quay.io/fedora/fedora-minimal:43 AS examplebuilder-stage\nRUN echo built > /built.txt\n",
		CopyArtifacts: []string{"COPY --from=examplebuilder-stage /built.txt /opt/examplebuilder-artifact"},
	})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the builder:examplebuilder capability + its self-contained CUE
// schema over the same channel a builtin uses; BuildCapabilities compiles the schema
// standalone, failing loudly if broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.175.0716",
		[]sdk.ProvidedCapability{{Class: "builder", Word: "examplebuilder", InputDef: "#ExamplebuilderInput"}},
		schemaFS, "schema")
}
