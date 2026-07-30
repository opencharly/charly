package main

// service_render.go — the HOST side of service materialization after the init
// de-type (Cutover F, leg 1). The init-system KNOWLEDGE — how a service_template /
// drop-in renders into a systemd unit or supervisord fragment, plus the restart/
// stdout policy mappings — lives in candy/plugin-init's OpResolve. The host builds
// the entry-derived, home-expanded ServiceRenderContext (pure ServiceEntry
// projection, no init knowledge) and calls the plugin, then egress-validates.
//
// Also carries the egress-validation dispatch (merged from the deleted charly/egress.go,
// coneB-buildtail dissolution): the validation logic + CUE schemas live in the compiled-in
// candy/plugin-egress; these functions resolve verb:egress and Invoke its OpValidate. The
// egress gate proves the config artifacts charly WRITES (cloud-init, k8s manifests, traefik
// routes, ledger JSON, the Containerfile, systemd/supervisord units, libvirt domain XML)
// BEFORE the bytes hit disk. host→plugin dispatch (plain resolve+Invoke, NOT the F10
// plugin→plugin reverse channel) — the pattern of credential_plugin.go. Compiled-in placement
// keeps it resolvable during build AND deploy with no connect step and no per-call gRPC cost.
// validateTextEgress below is this file's OWN live call (RenderService's unit-text gate); the
// init() function injects the host egress validator into sdk/kit's swappable ValidateRecord seam
// (sdk/kit cannot import charly core) — LOAD-BEARING wiring, not dead code, even though no other
// charly/*.go file calls ValidateEgress/ValidateEgressValue directly any more.
//
// The former vmshared.ValidateEgress / vmshared.UnmarshalEmbeddedDefaults host fills were DELETED
// (#55 vmshared Bucket D): their only consumers (vmshared.RenderCloudInit's egress gate,
// ovmf_paths' embedded-defaults parse) run EXCLUSIVELY in candy/plugin-vm, which fills those seams
// itself (candy/plugin-vm/vmshared_aliases.go). No charly-process path — direct or via any sdk kit
// charly imports — ever reaches them, so charly's fills were redundant; this drops charly's last
// sdk/vmshared import.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// Inject charly's egress-schema validation into the ledger's record-write path
// (sdk/kit has no egress subsystem — it calls the kit.ValidateRecord seam).
func init() { kit.ValidateRecord = ValidateEgressValue }

// egressValidate resolves the egress plugin and runs one OpValidate. mode ∈
// {bytes, text, xml}: "bytes" for serialized YAML/JSON (covers ValidateEgress + the
// marshalled ValidateEgressValue), "text" for a rendered non-data string, "xml" for the
// koala-decoded (best-effort) libvirt domain XML.
func egressValidate(kind, label, mode, data string) error {
	prov, ok := providerRegistry.resolve(ClassVerb, "egress")
	if !ok {
		return fmt.Errorf("%s: egress plugin (verb:egress) not registered — charly built without candy/plugin-egress", label)
	}
	reply, err := invokeTyped[map[string]string, egressReply](context.Background(), prov, "egress", OpValidate,
		map[string]string{"kind": kind, "label": label, "mode": mode, "data": data})
	if err != nil {
		return fmt.Errorf("%s: egress: %w", label, err)
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	return nil
}

// egressReply is verb:egress's OpValidate reply — a single error string ("" = valid).
type egressReply struct {
	Error string `json:"error"`
}

// ValidateEgress validates already-serialized YAML or JSON bytes against the egress kind's
// schema before they are written. JSON is a YAML subset, so one ingest path covers both.
func ValidateEgress(kind, label string, data []byte) error {
	return egressValidate(kind, label, "bytes", string(data))
}

// ValidateEgressValue validates an in-memory Go value (a manifest map[string]any, a record
// struct) by marshalling it to JSON and validating as bytes — faithful for the data values
// egress gates (k8s manifests, ledger records).
func ValidateEgressValue(kind, label string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%s: egress marshal value: %w", label, err)
	}
	return egressValidate(kind, label, "bytes", string(data))
}

// validateTextEgress validates a rendered NON-DATA text artifact (Containerfile, service
// unit) against the rendered_text string constraint (rejects the "<no value>" template marker).
func validateTextEgress(label, text string) error {
	return egressValidate("rendered_text", label, "text", text)
}

// The render types are shared spec envelopes (host builds them, plugin renders).
// ResolvedInit is the init de-type's build/label/entrypoint value envelope the
// kernel consumes instead of the concrete spec.Init.
type (
	ServiceRenderContext = spec.ServiceRenderContext
	RenderedService      = spec.RenderedService
	KeyValue             = spec.KeyValue
	ResolvedInit         = spec.ResolvedInit
)

// RenderService renders a ServiceEntry into a RenderedService via candy/plugin-init.
// (Transitional: the typed initDef is marshalled to the plugin; the init config goes
// fully opaque in F's finalize step.)
func RenderService(entry *spec.ServiceEntry, def *ResolvedInit, ctx ServiceRenderContext) (*RenderedService, error) {
	if entry == nil {
		return nil, fmt.Errorf("RenderService: nil entry")
	}
	if def == nil || def.ServiceSchema == nil {
		return nil, fmt.Errorf("RenderService: init system has no service_schema")
	}
	ctx = deploykit.BuildServiceRenderContext(entry, ctx)
	rendered, err := renderServiceViaPlugin(spec.ServiceRenderInput{Init: def.Raw, Ctx: ctx})
	if err != nil {
		return nil, err
	}
	// Egress gate (host-side): a template-render failure leaves the "<no value>"
	// marker in the unit body — reject it before the unit is written.
	if rendered.UnitText != "" {
		if err := validateTextEgress("service-unit:"+entry.Name, rendered.UnitText); err != nil {
			return nil, err
		}
	}
	return rendered, nil
}

// renderServiceViaPlugin invokes candy/plugin-init's OpResolve service-render leg.
func renderServiceViaPlugin(in spec.ServiceRenderInput) (*RenderedService, error) {
	out, err := invokeInitResolve(spec.InitResolveRequest{Render: &in})
	if err != nil {
		return nil, err
	}
	var reply spec.ServiceRenderReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("init render: decode reply: %w", err)
		}
	}
	if reply.Rendered == nil {
		return &RenderedService{}, nil
	}
	return reply.Rendered, nil
}

// resolveInitConfigViaPlugin invokes candy/plugin-init's OpResolve config leg,
// projecting one opaque init body into a *ResolvedInit (legs 2–4 value envelope).
func resolveInitConfigViaPlugin(body json.RawMessage) (*ResolvedInit, error) {
	out, err := invokeInitResolve(spec.InitResolveRequest{Config: &spec.InitResolveInput{Init: body}})
	if err != nil {
		return nil, err
	}
	var reply spec.InitResolveReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("init resolve config: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

// invokeInitResolve dispatches an OpResolve request to the compiled-in init kind
// provider (both legs share it).
func invokeInitResolve(req spec.InitResolveRequest) ([]byte, error) {
	prov, ok := providerRegistry.ResolveKind("init")
	if !ok {
		return nil, fmt.Errorf("init resolve: kind provider not registered")
	}
	return invokeTyped[spec.InitResolveRequest, json.RawMessage](context.Background(), prov, "init", OpResolve, req)
}
