// Package egress is the EGRESS VALIDATION plugin (M16): it gates the config artifacts
// charly WRITES to a system (cloud-init, k8s manifests, traefik routes, ledger JSON,
// systemd/quadlet units, the Containerfile, libvirt domain XML) against a CUE schema
// BEFORE the bytes hit disk — the egress counterpart to charly's ingress validation.
// The validation logic + the CUE schemas (formerly charly/egress.go + charly/schema/
// egress_*.cue + the vendored cloud_config) live HERE; charly's in-core ValidateEgress*
// functions are a thin shim that Invokes this plugin's OpValidate. Compiled-in (the
// build/deploy hot paths call it many times; an in-proc inprocProvider pays only a JSON
// envelope over a CUE-unify that dominates — gRPC per-call would be needless, and the
// perf-scoped build loader would not connect an out-of-process egress before generate).
//
// The schemas are held INTERNALLY (embedded + compiled in this plugin's own cue context,
// the package-less defs concatenated, the vendored cloud_config compiled as its own
// instance) and are NOT served over Describe — Describe ships only a trivial schema to
// satisfy the host's plugin-schema gate, so the vendored package+import file never has to
// join the single-blob Describe concat.
package egress

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/encoding/xml/koala"
	cueyaml "cuelang.org/go/encoding/yaml"

	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

//go:embed schema/*.cue
var describeSchemaFS embed.FS

//go:embed egress-schemas/*.cue egress-schemas/vendor/*.cue
var egressSchemaFS embed.FS

const calver = "2026.181.0001"

// kindDefPaths maps each egress kind to its CUE def path among the package-less egress
// schemas (the vendored cloud_config is compiled + registered separately).
var kindDefPaths = map[string]string{
	"rendered_text":      "#RenderedText",
	"traefik_routes":     "#TraefikRoutes",
	"k8s_object":         "#K8sObject",
	"kustomization":      "#Kustomization",
	"deploy_record":      "#DeployRecord",
	"candy_record":       "#CandyRecord",
	"cloud_init_meta":    "#CloudInitMeta",
	"cloud_init_net":     "#NetworkConfigV2",
	"libvirt_domain_xml": "#LibvirtDomainXML",
}

// NewProvider builds the egress provider, compiling its schemas once at construction.
func NewProvider() pb.ProviderServer {
	p, err := newProvider()
	if err != nil {
		panic("plugin-egress: " + err.Error())
	}
	return p
}

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct {
	pb.UnimplementedProviderServer
	ctx  *cue.Context
	defs map[string]cue.Value
}

func newProvider() (*provider, error) {
	ctx := cuecontext.New()
	// The package-less egress schemas concatenate into ONE instance (import-free, exactly
	// as charly's sharedCueSchema joined them); the vendored cloud_config carries a
	// package clause + CUE-stdlib imports, so it compiles as its OWN instance.
	var b strings.Builder
	ents, err := egressSchemaFS.ReadDir("egress-schemas")
	if err != nil {
		return nil, fmt.Errorf("read egress-schemas: %w", err)
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cue") {
			continue
		}
		data, err := egressSchemaFS.ReadFile("egress-schemas/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		b.Write(data)
		b.WriteString("\n")
	}
	shared := ctx.CompileString(b.String())
	if shared.Err() != nil {
		return nil, fmt.Errorf("compile egress schemas: %v", errors.Details(shared.Err(), nil))
	}
	defs := map[string]cue.Value{}
	for kind, path := range kindDefPaths {
		d := shared.LookupPath(cue.ParsePath(path))
		if d.Err() != nil {
			return nil, fmt.Errorf("egress kind %q: def %s not found: %v", kind, path, d.Err())
		}
		defs[kind] = d
	}
	// Vendored cloud_config — its own instance (package + imports resolve in the bare ctx).
	vdata, err := egressSchemaFS.ReadFile("egress-schemas/vendor/cloud_config.cue")
	if err != nil {
		return nil, fmt.Errorf("read vendored cloud_config: %w", err)
	}
	vv := ctx.CompileBytes(vdata)
	if vv.Err() != nil {
		return nil, fmt.Errorf("compile vendored cloud_config: %v", errors.Details(vv.Err(), nil))
	}
	cc := vv.LookupPath(cue.ParsePath("#CloudConfig"))
	if cc.Err() != nil {
		return nil, fmt.Errorf("vendored cloud_config: #CloudConfig not found: %v", cc.Err())
	}
	defs["cloud_config"] = cc
	return &provider{ctx: ctx, defs: defs}, nil
}

// validateInput is the OpValidate plugin_input: validate `data` against the egress `kind`.
// mode selects the validation form: "bytes" (serialized YAML/JSON — the default, covering
// both ValidateEgress and the marshalled ValidateEgressValue), "text" (a rendered non-data
// string against the rendered_text string constraint), or "xml" (koala-decoded, best-effort).
type validateInput struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Mode  string `json:"mode"`
	Data  string `json:"data"`
}

// validateReply carries the validation verdict: Error == "" means valid.
type validateReply struct {
	Error string `json:"error"`
}

// Invoke handles OpValidate: validate the egress artifact, returning a {error} verdict
// (empty == valid). The host shim turns a non-empty error into a Go error and aborts the write.
func (p *provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpValidate {
		return nil, fmt.Errorf("egress: unsupported op %q (only %q)", req.GetOp(), sdk.OpValidate)
	}
	var in validateInput
	if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
		return nil, fmt.Errorf("egress: decode input: %w", err)
	}
	out, err := json.Marshal(validateReply{Error: p.validate(in)})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// validate runs the kind's schema against the data; returns "" on success or the
// validation-failure message. Mirrors the former charly/egress.go validators exactly.
func (p *provider) validate(in validateInput) string {
	def, ok := p.defs[in.Kind]
	if !ok {
		return fmt.Sprintf("%s: no egress schema registered for kind %q", in.Label, in.Kind)
	}
	switch in.Mode {
	case "text":
		// #RenderedText is a string constraint (rejects the "<no value>" template marker);
		// no concreteness requirement.
		v := p.ctx.Encode(in.Data)
		if v.Err() != nil {
			return fmt.Sprintf("%s: text egress encode: %v", in.Label, v.Err())
		}
		if err := v.Unify(def).Validate(); err != nil {
			return fmt.Sprintf("%s: text egress validation failed:\n%s", in.Label, errors.Details(err, nil))
		}
	case "xml":
		// koala is EXPERIMENTAL + best-effort: a decode/build failure defers to the
		// authoritative downstream gate (libvirt DomainDefineXML), only a schema violation
		// on a decoded document hard-fails.
		expr, err := koala.NewDecoder(in.Label, strings.NewReader(in.Data)).Decode()
		if err != nil {
			return ""
		}
		v := p.ctx.BuildExpr(expr)
		if v.Err() != nil {
			return ""
		}
		if err := v.Unify(def).Validate(cue.Concrete(true)); err != nil {
			return fmt.Sprintf("%s: XML egress validation failed:\n%s", in.Label, errors.Details(err, nil))
		}
	default: // "bytes": serialized YAML/JSON (JSON is a YAML subset — one ingest path)
		af, err := cueyaml.Extract(in.Label, []byte(in.Data))
		if err != nil {
			return fmt.Sprintf("%s: egress ingest: %v", in.Label, err)
		}
		v := p.ctx.BuildFile(af)
		if v.Err() != nil {
			return fmt.Sprintf("%s: egress build: %v", in.Label, v.Err())
		}
		if err := v.Unify(def).Validate(cue.Concrete(true)); err != nil {
			return fmt.Sprintf("%s: egress validation failed:\n%s", in.Label, errors.Details(err, nil))
		}
	}
	return ""
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises verb:egress serving OpValidate. The egress SCHEMAS are internal
// (compiled in newProvider) — Describe ships only the trivial #EgressInput so the host's
// plugin-schema gate has a non-empty, base-spliceable schema.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "verb", Word: "egress"}},
		describeSchemaFS, "schema")
}
