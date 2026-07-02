// Command plugin-example-step is a reference OUT-OF-TREE charly plugin that proves
// BOTH legs of operator-authorized plugin execution for a VERB-as-step:
//
//   - BUILD-context (OpEmit): `charly box build`/`generate` connects it
//     out-of-process and Invokes its OpEmit during image generation, then splices the
//     returned Containerfile FRAGMENT verbatim into the .build/<image>/Containerfile —
//     so the RUN bakes a marker file into the image.
//   - DEPLOY-context (OpExecute): a `run: plugin: examplestep` step composed in a
//     LOCAL/VM deploy is lowered to charly's ExternalPluginStep, which Invokes this
//     plugin's OpExecute WITH the host's live executor on the go-plugin broker (the
//     E3b reverse channel). The plugin dials back through the SDK (ExecutorFromInvoke)
//     and writes a marker on the target VENUE, then RETURNS a DeployReply carrying a
//     plugin-script reverse op the host RECORDS in the ledger and REPLAYS at
//     `charly bundle del` — the deploy-time counterpart of the build-context bake.
//
// Authored as a candy `run:` step (`plugin: examplestep`, a verb), it is the
// verb-as-step analogue of the deploy-TARGET candy/plugin-example-deploy.
//
// The operator-authorized plugin-execution MECHANISM lives in charly (the
// generate.go NewGenerator build-connect seam + tasks.go emitPluginFragment for
// build; loadDeployPlugins + ExternalPluginStep over the E3b reverse channel for
// deploy); this module is only the reference PAYLOAD that mechanism builds + executes.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// opEmit / opExecute mirror charly's op selectors (package main's OpEmit = "emit",
// OpExecute = "execute"). An external plugin can't import those constants, so they are
// named here; the host sends opEmit on the BUILD-context Invoke and opExecute on the
// DEPLOY-context Invoke.
const (
	opEmit    = "emit"
	opExecute = "execute"
)

// markerDir derives the deploy's disposable scratch dir DETERMINISTICALLY from the
// step's marker (its plugin_input), so the deploy-context check that asserts the
// marker has a stable, author-controlled path. Under /tmp → zero operator
// side-effect; the recorded reverse op removes it. Sanitized so an arbitrary marker
// can never escape /tmp/charly-examplestep/.
func markerDir(marker string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, marker)
	if safe == "" {
		safe = "default"
	}
	return path.Join("/tmp/charly-examplestep", safe)
}

// Invoke handles BOTH op selectors:
//
//   - opEmit (build): returns a spec.EmitReply whose Fragment is a Containerfile RUN
//     the host splices verbatim, baking an empty /opt/examplestep-baked marker into the
//     image (proof the plugin executed at build). The host marshals plugin_input as
//     op.Params and a spec.BuildEnv as op.Env; this example emits a static RUN.
//   - opExecute (deploy): applies a marker on the target VENUE via the E3b reverse
//     channel and returns the teardown ops + ledger record (see execProvision).
//
// Any other op returns a benign empty result.
func (p provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case opEmit:
		j, err := json.Marshal(spec.EmitReply{Fragment: "RUN mkdir -p /opt && : > /opt/examplestep-baked\n"})
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: j}, nil
	case opExecute:
		return p.execProvision(ctx, req)
	default:
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	}
}

// stepInput is the UNWRAPPED plugin_input the host marshals into op.Params for an
// OpExecute Invoke (the SAME unwrapped shape the build-context OpEmit / the external
// deploy target use). The marker keys the venue scratch dir.
type stepInput struct {
	Marker string `json:"marker"`
}

// execProvision is the DEPLOY-context act: dial the host's executor over the E3b
// reverse channel, decode the step's plugin_input (and venue, proving both travel),
// write the applied + probe markers on the target VENUE under a disposable /tmp
// scratch dir (user scope, no sudo), and return a single generic plugin-script reverse
// op removing the whole dir at teardown. Mirrors candy/plugin-example-deploy's apply,
// but driven by a verb-as-step (ExternalPluginStep) rather than a deploy substrate.
func (provider) execProvision(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, err
	}
	var in stepInput
	if raw := req.GetParamsJson(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("plugin-example-step: decode plugin_input: %w", err)
		}
	}
	// Decode the venue too — proves the DeployVenue descriptor travels over the
	// step OpExecute wire (the deploy-context analogue of spec.BuildEnv). The marker
	// keys the path (author-controlled, deterministic for the bed check); the venue
	// name is the fallback when no marker is authored.
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-example-step: decode venue: %w", err)
	}
	name := in.Marker
	if name == "" {
		name = venue.DeployName
	}
	dir := markerDir(name)
	applied := dir + "/applied"
	probe := dir + "/probe"
	apply := "mkdir -p " + dir + " && : > " + applied + " && : > " + probe
	if err := exec.RunUser(ctx, apply, nil); err != nil {
		return nil, err
	}
	reverse := sdk.PluginScriptReverseOp(spec.ScopeUser, "rm -rf "+dir)
	return sdk.BuildDeployReply([]spec.ReverseOp{reverse}, "plugin-example-step", "2026.175.1200")
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the verb:examplestep capability + its self-contained CUE
// schema over the same channel a builtin uses; BuildCapabilities compiles the schema
// standalone, failing loudly if broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.175.1200",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "examplestep", InputDef: "#ExamplestepInput"}},
		schemaFS, "schema")
}
