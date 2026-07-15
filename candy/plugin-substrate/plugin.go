// Package substratekind is the importable form of charly's 5 SUBSTRATE structural KINDs —
// pod / vm / k8s / local / android — relocated out of charly's module (C2-substrate; formerly
// the shared built-in standaloneKind in charly/plugin_substrate.go). ONE provider serves all
// 5 words; Describe advertises each with Structural:true.
//
// PURE-ECHO seam. Unlike group (candy/plugin-group), a substrate value is RICH +
// core-referencing (#Vm/#Deploy/#LibvirtDomain/… with host-canonicalized shorthand like
// tunnel:/port:), so it cannot be re-decoded from op.Params by a plugin nor validated by a
// self-contained plugin schema. So the HOST pre-decodes the CANONICAL node via the core loader
// (buildBundleNode for the deploy shape, decodeNodeValue for the template shape — the SINGLE
// decode source of truth, R3), validates its value host-side against the KEPT #<Kind>Value def,
// and threads the result in op.Env (spec.StructuralKindLoadEnv.Standalone). This OpLoad simply
// RETURNS it: a deploy echo (spec.Deploy) the host folds into uf.Bundle, or a template echo
// (the per-substrate typed value's JSON) the host folds into uf.Pod/uf.VM/… — the C2-substrate
// TEMPLATE fold arm that extends F5's deploy-only fold. RDD proved a canonical spec.Deploy /
// spec.Vm / spec.Pod / … round-trips through JSON byte-faithfully, so this thread-echo-fold is
// BYTE-EQUIVALENT to the former in-proc standaloneKind decode (buildBundleNodeInto /
// buildStandaloneResource).
//
// PLACEMENT — COMPILED-IN (listed in the embedded charly/charly.yml compiled_plugins:), NOT
// external. The 5 substrate kinds are CORE deploy primitives that must ALWAYS resolve: every
// box/submodule authoring a pod:/vm:/k8s:/local:/android: node (the root check/vm/local/k8s
// entities, box/fedora, box/cachyos, box/arch, box/debian, box/ubuntu) relies on them without
// discovering this candy, exactly like the tier-1 kinds and group. (cmd/serve serves it
// out-of-process too — one provider, two placements, zero authoring change.)
package substratekind

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

const calver = "2026.196.0600"

// substrateWords is the ONE list of words this provider serves — pod/vm/k8s/local/android.
var substrateWords = []string{"pod", "vm", "k8s", "local", "android"}

// substrateTraits is the per-word DECLARED #DeployTraits (P9) — the SINGLE source the kernel
// consults for each substrate's deploy behaviour. kit.StampDescent stamps these onto every
// node's spec.DescentDescriptor (resolved by the host's registry-backed deployTraitsFor), so
// every consult site reads the behaviour off node.Descent BY TRAIT — never by switching on the
// kind word. Canonical table (Appendix B): pod=container+image_backed+image_context;
// vm=ssh+machine_venue+exclusive_venue; local=shell+machine_venue; k8s=shell+image_context+
// leaf_only; android=parent; a zero-value word = external-in-place.
var substrateTraits = map[string]*spec.DeployTraits{
	"pod":     {Venue: "container", ImageBacked: true, ImageContext: true},
	"vm":      {Venue: "ssh", MachineVenue: true, ExclusiveVenue: true},
	"local":   {Venue: "shell", MachineVenue: true},
	"k8s":     {Venue: "shell", ImageContext: true, LeafOnly: true},
	"android": {Venue: "parent"},
}

// NewProvider returns the substrate kind provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the 5 STRUCTURAL substrate kind capabilities (Class "kind",
// Structural:true) + the self-contained CUE schema (via sdk.NewMeta → BuildCapabilities).
// Each declares InputDef:"" — the rich substrate value is validated HOST-SIDE against the
// KEPT #<Kind>Value core def (runPluginKind → validateStandaloneKindValueCUE), NOT by this
// served schema. The self-contained #SubstrateKindLoad def exists only to satisfy the
// non-empty-schema load gate + document the seam.
func NewMeta() pb.PluginMetaServer {
	caps := make([]sdk.ProvidedCapability, 0, len(substrateWords))
	for _, w := range substrateWords {
		caps = append(caps, sdk.ProvidedCapability{Class: "kind", Word: w, Structural: true, DeployTraits: substrateTraits[w]})
	}
	return sdk.NewMeta(calver, caps,
		nil)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpLoad for a substrate structural kind. The host has already pre-decoded the
// CANONICAL node and threaded it in op.Env (spec.StructuralKindLoadEnv.Standalone). This ECHOES
// it: for the deploy shape, marshal the pre-decoded spec.Deploy back (→ host folds uf.Bundle);
// for the template shape, return the pre-decoded typed template JSON verbatim (→ host folds the
// typed map). The op.Params body is deliberately IGNORED — a substrate value cannot be soundly
// re-decoded from the raw op.Params (host-canonicalized shorthand), which is why the host
// pre-decodes and threads via op.Env.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpLoad:
		return substrateLoad(req)
	case sdk.OpResolve:
		// The substrate-template de-type (Cutover I): project an opaque local:/android:
		// TEMPLATE body into a Resolved* envelope the kernel consumes.
		var in spec.SubstrateTemplateResolveRequest
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("substrate template resolve: decode input: %w", err)
			}
		}
		out, err := resolveSubstrateTemplate(in)
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpStatusCollect:
		// P14a: the substrate COLLECTOR OpStatus. The host's status fan-out
		// reaches the cleanly-movable collectors (pod live + local) here, by
		// word (pod/vm/k8s/local/android). vm/k8s/android defer to K5.
		res, err := statusCollect(ctx, req.GetReserved(), req.GetParamsJson())
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: res.json}, nil
	default:
		return nil, fmt.Errorf("substrate kind %q: unsupported op %q", req.GetReserved(), req.GetOp())
	}
}

func substrateLoad(req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var env spec.StructuralKindLoadEnv
	if len(req.GetEnvJson()) > 0 {
		if err := json.Unmarshal(req.GetEnvJson(), &env); err != nil {
			return nil, fmt.Errorf("substrate kind %q: decode load env: %w", req.GetReserved(), err)
		}
	}
	if env.Standalone == nil {
		return nil, fmt.Errorf("substrate kind %q: host threaded no pre-decoded node (op.Env.standalone missing)", req.GetReserved())
	}
	switch env.Standalone.Shape {
	case "template":
		if len(env.Standalone.Template) == 0 {
			return nil, fmt.Errorf("substrate kind %q: template shape carries no template", req.GetReserved())
		}
		// Echo the host-pre-decoded typed template value verbatim (raw JSON) — the host folds
		// it into uf.Pod/uf.VM/… by kind.
		return &pb.InvokeReply{ResultJson: env.Standalone.Template}, nil
	case "deploy":
		if env.Standalone.Deploy == nil {
			return nil, fmt.Errorf("substrate kind %q: deploy shape carries no deploy node", req.GetReserved())
		}
		out, err := json.Marshal(env.Standalone.Deploy)
		if err != nil {
			return nil, fmt.Errorf("substrate kind %q: marshal deploy: %w", req.GetReserved(), err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	default:
		return nil, fmt.Errorf("substrate kind %q: unknown load shape %q (want deploy|template)", req.GetReserved(), env.Standalone.Shape)
	}
}
