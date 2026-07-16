package main

// egress.go — the in-core SHIM for egress validation (M16). The validation logic + the
// CUE schemas (incl. the vendored cloud_config) live in the COMPILED-IN candy/plugin-egress;
// these four public functions resolve verb:egress and Invoke its OpValidate, so every
// existing caller (generate.go, k8s_generate.go, install_ledger.go, service_render.go,
// vm_create_spec.go) AND the vmshared.ValidateEgress hook are unchanged. The egress gate
// proves the config artifacts charly WRITES (cloud-init, k8s manifests, traefik routes,
// ledger JSON, the Containerfile, systemd/supervisord units, libvirt domain XML) BEFORE the
// bytes hit disk — the output counterpart to the CUE INGRESS validation in cue_schema.go.
//
// host→plugin dispatch (plain resolve+Invoke, NOT the F10 plugin→plugin reverse channel) —
// the pattern of k8s_plugin.go / credential_plugin.go. Compiled-in placement keeps it
// resolvable during build AND deploy (and the host-side *Via / VM validations) with no
// connect step and no per-call gRPC cost.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencharly/sdk/kit"
)

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

// Inject charly's egress-schema validation into the ledger's record-write path
// (sdk/kit has no egress subsystem — it calls the kit.ValidateRecord seam).
func init() { kit.ValidateRecord = ValidateEgressValue }

// ValidateXMLEgress validates a rendered XML artifact (the libvirt domain XML); the plugin
// koala-decodes it best-effort (a decode failure defers to libvirt's authoritative gate).
func ValidateXMLEgress(kind, label, xmlStr string) error {
	return egressValidate(kind, label, "xml", xmlStr)
}
