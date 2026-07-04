// Package candykind is the importable form of charly's `candy` box⊻layer factory KIND — the LAST
// structural kind relocated out of charly's module (C2-candy; formerly the built-in candyKind in
// charly/plugin_candy.go). It serves ONE word (`kind:candy`; Structural is FALSE — candy nests no
// deploy members, and it is routed to the host's foldCandyKind by an explicit disc branch).
//
// PURE-ECHO seam (mirrors candy/plugin-substrate). A `candy:` value is RICH + core-referencing
// (#Candy/#Box with host-canonicalized shorthand), so it can neither ride op.Params nor be
// validated by a self-contained plugin schema. So the HOST pre-decodes the CANONICAL box⊻layer
// node via the BOOTSTRAP-CRITICAL core candyIsImage + buildCandy (the SINGLE decode source that
// STAYS core — the discovered-candy pre-check in unified.go calls it DIRECTLY), validates the value
// against the KEPT #CandyValue def, and threads the result in op.Env
// (spec.StructuralKindLoadEnv.Standalone: Shape "candy-image" → spec.Box for a full IMAGE,
// "candy-layer" → spec.Candy for a LAYER fragment). This OpLoad simply RETURNS it: the host folds
// the echo into uf.Box (image) or uf.Candy (layer), byte-equivalent to the former in-proc candyKind
// decode. RDD proved a canonical spec.Box / spec.Candy round-trips through JSON byte-faithfully.
//
// PLACEMENT — COMPILED-IN (listed in the embedded charly/charly.yml compiled_plugins:), NOT
// external. This is what dissolves the historical "candy is bootstrap-loader-core" blocker: the
// blocker was a bootstrap CYCLE — an EXTERNAL candy plugin would need fetching+building by
// discovering the very candies it lives among. A COMPILED-IN plugin has NO fetch/build; it
// registers at init() (plugins_generated.go), BEFORE any LoadUnified runs, so the discovered-candy
// pre-check + every inline candy decode resolve it. candy is THE core entity — every box/submodule
// must always resolve it — exactly why it is compiled in, like group + the substrates + the tier-1
// kinds. (cmd/serve serves it out-of-process too — one provider, two placements.)
//
// NOTE the sibling candy/plugin-candy is a DIFFERENT plugin (the `command:candy` CLI authoring
// surface); this one is `kind:candy`. Distinct classes, distinct dirs.
package candykind

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

const calver = "2026.182.1600"

// NewProvider returns the candy kind provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta ships the candy kind capability (Class "kind", word "candy", InputDef:"" — the rich
// box⊻layer value is validated HOST-SIDE against the KEPT #CandyValue core def, NOT by this
// served schema; the self-contained #CandyKindLoad def exists only to satisfy the
// non-empty-schema load gate), via sdk.NewMeta → BuildCapabilities. Structural is deliberately
// omitted (FALSE): `candy` nests NO deploy resource members and is routed to foldCandyKind by an
// explicit `gn.disc=="candy"` host branch, NOT via the structural fold.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "kind", Word: "candy"}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpLoad for the candy box⊻layer factory kind. The host has already pre-decoded the
// CANONICAL node (candyIsImage + buildCandy) and threaded it in op.Env
// (spec.StructuralKindLoadEnv.Standalone). This ECHOES it: candy-image → marshal the pre-decoded
// spec.Box back (→ host folds uf.Box); candy-layer → marshal the pre-decoded spec.Candy back (→
// host folds uf.Candy). op.Params is deliberately IGNORED — a candy value cannot be soundly
// re-decoded from raw op.Params (host-canonicalized shorthand + the box⊻layer routing), which is
// why the host pre-decodes and threads via op.Env.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpLoad {
		return nil, fmt.Errorf("candy kind: unsupported op %q (only %q)", req.GetOp(), sdk.OpLoad)
	}
	var env spec.StructuralKindLoadEnv
	if len(req.GetEnvJson()) > 0 {
		if err := json.Unmarshal(req.GetEnvJson(), &env); err != nil {
			return nil, fmt.Errorf("candy kind: decode load env: %w", err)
		}
	}
	if env.Standalone == nil {
		return nil, fmt.Errorf("candy kind: host threaded no pre-decoded node (op.Env.standalone missing)")
	}
	switch env.Standalone.Shape {
	case "candy-image":
		if env.Standalone.Box == nil {
			return nil, fmt.Errorf("candy kind: candy-image shape carries no box node")
		}
		out, err := json.Marshal(env.Standalone.Box)
		if err != nil {
			return nil, fmt.Errorf("candy kind: marshal box: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	case "candy-layer":
		if env.Standalone.Candy == nil {
			return nil, fmt.Errorf("candy kind: candy-layer shape carries no candy node")
		}
		out, err := json.Marshal(env.Standalone.Candy)
		if err != nil {
			return nil, fmt.Errorf("candy kind: marshal candy: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	default:
		return nil, fmt.Errorf("candy kind: unknown load shape %q (want candy-image|candy-layer)", env.Standalone.Shape)
	}
}
