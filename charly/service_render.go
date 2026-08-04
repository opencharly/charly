package main

// service_render.go — the egress-validation dispatch (merged from the deleted
// charly/egress.go, coneB-buildtail dissolution): the validation logic + CUE schemas
// live in the compiled-in candy/plugin-egress; these functions resolve verb:egress and
// Invoke its ops.OpValidate. The egress gate proves the config artifacts charly WRITES
// (cloud-init, k8s manifests, traefik routes, ledger JSON, the Containerfile,
// systemd/supervisord units, libvirt domain XML) BEFORE the bytes hit disk. host→plugin
// dispatch (plain resolve+Invoke, NOT the F10 plugin→plugin reverse channel) — the
// pattern of credential_plugin.go. Compiled-in placement keeps it resolvable during
// build AND deploy with no connect step and no per-call gRPC cost. The init() function
// injects the host egress validator into sdk/kit's swappable ValidateRecord seam
// (sdk/kit cannot import charly core) — LOAD-BEARING wiring, not dead code, even though
// no other charly/*.go file calls ValidateEgress/ValidateEgressValue directly any more
// (its sole production reach is the install-ledger record-write path, via
// spec.ValidateRecord — candy/plugin-bundle, compiled-in, is the sole ledger writer for
// every substrate, sharing this process, so the seam is always correctly wired; verified
// #55 W3 B4).
//
// The former vmshared.ValidateEgress / vmshared.UnmarshalEmbeddedDefaults host fills were DELETED
// (#55 vmshared Bucket D): their only consumers (vmshared.RenderCloudInit's egress gate,
// ovmf_paths' embedded-defaults parse) run EXCLUSIVELY in candy/plugin-vm, which fills those seams
// itself (candy/plugin-vm/vmshared_aliases.go). No charly-process path — direct or via any sdk kit
// charly imports — ever reaches them, so charly's fills were redundant; this drops charly's last
// sdk/vmshared import.
//
// RenderService/renderServiceViaPlugin (the HOST side of systemd-service materialization
// via candy/plugin-init's OpResolve) are DELETED (#55 W3 B4): their "TWO registry
// consults a plugin cannot do itself" framing was stale — kind:init's OpResolve and
// verb:egress's OpValidate are both compiled-in, reachable via direct InvokeProvider
// peer dispatch, so the whole render (build ServiceRenderContext, resolve via
// candy/plugin-init, egress-validate the unit text) now lives in
// sdk/deploykit/compile_service_steps.go + render_generator_from_project.go's
// renderSeamCaller.renderService — no host round-trip left at all. validateTextEgress
// (their unit-text egress-gate wrapper) died with them — its own test now calls
// egressValidate directly (egress_test.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// Inject charly's egress-schema validation into the ledger's record-write path
// (the sdk libraries have no egress subsystem — the ledger calls the spec.ValidateRecord
// seam, which charly fills here at init).
func init() { spec.ValidateRecord = ValidateEgressValue }

// egressValidate resolves the egress plugin and runs one ops.OpValidate. mode ∈
// {bytes, text, xml}: "bytes" for serialized YAML/JSON (covers ValidateEgress + the
// marshalled ValidateEgressValue), "text" for a rendered non-data string, "xml" for the
// koala-decoded (best-effort) libvirt domain XML.
func egressValidate(kind, label, mode, data string) error {
	prov, ok := providerRegistry.resolve(ClassVerb, "egress")
	if !ok {
		return fmt.Errorf("%s: egress plugin (verb:egress) not registered — charly built without candy/plugin-egress", label)
	}
	reply, err := invokeTyped[map[string]string, egressReply](context.Background(), prov, "egress", ops.OpValidate,
		map[string]string{"kind": kind, "label": label, "mode": mode, "data": data})
	if err != nil {
		return fmt.Errorf("%s: egress: %w", label, err)
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	return nil
}

// egressReply is verb:egress's ops.OpValidate reply — a single error string ("" = valid).
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

// resolveInitConfigViaPlugin invokes candy/plugin-init's ops.OpResolve config leg,
// projecting one opaque init body into a *spec.ResolvedInit (legs 2–4 value envelope).
func resolveInitConfigViaPlugin(body json.RawMessage) (*spec.ResolvedInit, error) {
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

// invokeInitResolve dispatches an ops.OpResolve request to the compiled-in init kind
// provider (both legs share it).
func invokeInitResolve(req spec.InitResolveRequest) ([]byte, error) {
	prov, ok := providerRegistry.ResolveKind("init")
	if !ok {
		return nil, fmt.Errorf("init resolve: kind provider not registered")
	}
	return invokeTyped[spec.InitResolveRequest, json.RawMessage](context.Background(), prov, "init", ops.OpResolve, req)
}
