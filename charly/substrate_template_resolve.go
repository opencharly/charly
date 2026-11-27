package main

// substrate_template_resolve.go — the HOST side of the local + vm substrate
// TEMPLATES after the substrate-template de-type (Cutover I). The kernel stores
// local:/vm: template bodies opaquely (uf.Local / uf.Vm are
// map[string]json.RawMessage) and consumes candy/plugin-substrate's ops.OpResolve
// projection (spec.ResolvedLocal / spec.ResolvedVm) — never spec.Local / spec.Vm.
// (W0: the former ResolvedLocal/ResolvedKubernetes in-package aliases are deleted — every
// caller reads spec.* directly; resolveKubernetesViaPlugin died with its only caller, findKubernetesSpec,
// relocated into the former host_build_deploy_entity_resolve.go's kind-blind resolveEntityTemplate
// — itself deleted now, K-wave W3a A3-phase-2: every kind:<word> caller self-loads the project
// plugin-side via sdk/loaderkit's Resolve{Kubernetes,Vm}EntityViaExecutor instead.)

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// resolveVmViaPlugin projects one opaque vm template body into a *spec.ResolvedVm
// via candy/plugin-substrate's ops.OpResolve leg (the vm
// substrate-value de-type, Cutover L). Returns nil for an empty/absent body.
func resolveVmViaPlugin(body json.RawMessage) (*spec.ResolvedVm, error) {
	if len(body) == 0 {
		return nil, nil
	}
	out, err := invokeSubstrateTemplateResolve(spec.SubstrateTemplateResolveRequest{
		Vm: &spec.VmResolveInput{Vm: body},
	})
	if err != nil {
		return nil, err
	}
	var reply spec.VmResolveReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("vm resolve: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

// resolveLocalViaPlugin projects one opaque local template body into a *spec.ResolvedLocal
// via candy/plugin-substrate's ops.OpResolve leg.
func resolveLocalViaPlugin(body json.RawMessage) (*spec.ResolvedLocal, error) {
	out, err := invokeSubstrateTemplateResolve(spec.SubstrateTemplateResolveRequest{
		Local: &spec.LocalResolveInput{Local: body},
	})
	if err != nil {
		return nil, err
	}
	var reply spec.LocalResolveReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("local resolve: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

func invokeSubstrateTemplateResolve(req spec.SubstrateTemplateResolveRequest) ([]byte, error) {
	// The substrate provider serves every substrate word; any resolves the template legs.
	prov, ok := providerRegistry.ResolveKind("local")
	if !ok {
		return nil, fmt.Errorf("substrate template resolve: kind provider not registered")
	}
	return invokeTyped[spec.SubstrateTemplateResolveRequest, json.RawMessage](context.Background(), prov, "local", ops.OpResolve, req)
}
