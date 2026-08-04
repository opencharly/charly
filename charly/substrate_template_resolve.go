package main

// substrate_template_resolve.go — the HOST side of the local + android substrate
// TEMPLATES after the substrate-template de-type (Cutover I). The kernel stores
// local:/android: template bodies opaquely (uf.Local / uf.Android are
// map[string]json.RawMessage) and consumes candy/plugin-substrate's ops.OpResolve
// projection (spec.ResolvedLocal / spec.ResolvedAndroid) — never spec.Local / spec.Android.
// (W0: the former ResolvedLocal/ResolvedAndroid/ResolvedK8s in-package aliases are deleted — every
// caller reads spec.* directly; resolveK8sViaPlugin died with its only caller, findK8sSpec,
// relocated into the former host_build_deploy_entity_resolve.go's kind-blind resolveEntityTemplate
// — itself deleted now, K-wave W3a A3-phase-2: every kind:<word> caller self-loads the project
// plugin-side via sdk/loaderkit's Resolve{K8s,Vm,Android}EntityViaExecutor instead.)

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

// resolveAndroidViaPlugin projects one opaque android template body into a
// *spec.ResolvedAndroid.
func resolveAndroidViaPlugin(body json.RawMessage) (*spec.ResolvedAndroid, error) {
	out, err := invokeSubstrateTemplateResolve(spec.SubstrateTemplateResolveRequest{
		Android: &spec.AndroidResolveInput{Android: body},
	})
	if err != nil {
		return nil, err
	}
	var reply spec.AndroidResolveReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("android resolve: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

func invokeSubstrateTemplateResolve(req spec.SubstrateTemplateResolveRequest) ([]byte, error) {
	// The substrate provider serves all 5 words; any resolves the template legs.
	prov, ok := providerRegistry.ResolveKind("local")
	if !ok {
		return nil, fmt.Errorf("substrate template resolve: kind provider not registered")
	}
	return invokeTyped[spec.SubstrateTemplateResolveRequest, json.RawMessage](context.Background(), prov, "local", ops.OpResolve, req)
}

// resolveAndroidViaPlugin is the android RESOLVE callback the host threads into
// loaderkit.ValidateAndroidDevices (the box⊻adb XOR validator relocated to
// sdk/loaderkit); its former map-shaped wrapper resolveAndroids (the sole caller of which was the
// relocated validateAndroidDevices) is deleted with the move. Its sibling resolveLocals died with
// the validate ENGINE (task #60 — the host's only caller, validateLocalTemplates, moved to
// plugin-box, which decodes Templates.Local itself off the resolved-project envelope).
