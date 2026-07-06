package main

// substrate_template_resolve.go — the HOST side of the local + android substrate
// TEMPLATES after the substrate-template de-type (Cutover I). The kernel stores
// local:/android: template bodies opaquely (uf.Local / uf.Android are
// map[string]json.RawMessage) and consumes candy/plugin-substrate's OpResolve
// projection (ResolvedLocal / ResolvedAndroid) — never spec.Local / spec.Android.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// ResolvedLocal / ResolvedAndroid / ResolvedPod / ResolvedK8s are the substrate-template value envelopes.
type (
	ResolvedLocal   = spec.ResolvedLocal
	ResolvedAndroid = spec.ResolvedAndroid
	ResolvedPod     = spec.ResolvedPod
	ResolvedK8s     = spec.ResolvedK8s
)

// resolveK8sViaPlugin projects one opaque k8s cluster template body into a *ResolvedK8s
// via candy/plugin-substrate's OpResolve leg (the k8s substrate-value de-type, Cutover K).
func resolveK8sViaPlugin(body json.RawMessage) (*ResolvedK8s, error) {
	out, err := invokeSubstrateTemplateResolve(spec.SubstrateTemplateResolveRequest{
		K8s: &spec.K8sResolveInput{K8s: body},
	})
	if err != nil {
		return nil, err
	}
	var reply spec.K8sResolveReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("k8s resolve: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

// resolveVmViaPlugin projects one opaque vm template body into a *VmSpec
// (= *spec.ResolvedVm) via candy/plugin-substrate's OpResolve leg (the vm
// substrate-value de-type, Cutover L). Returns nil for an empty/absent body.
func resolveVmViaPlugin(body json.RawMessage) (*VmSpec, error) {
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

// resolvePodViaPlugin projects one opaque pod template body into a *ResolvedPod
// via candy/plugin-substrate's OpResolve leg (the pod-template de-type, Cutover J).
func resolvePodViaPlugin(body json.RawMessage) (*ResolvedPod, error) {
	out, err := invokeSubstrateTemplateResolve(spec.SubstrateTemplateResolveRequest{
		Pod: &spec.PodResolveInput{Pod: body},
	})
	if err != nil {
		return nil, err
	}
	var reply spec.PodResolveReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("pod resolve: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

// resolveLocalViaPlugin projects one opaque local template body into a *ResolvedLocal
// via candy/plugin-substrate's OpResolve leg.
func resolveLocalViaPlugin(body json.RawMessage) (*ResolvedLocal, error) {
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
// *ResolvedAndroid.
func resolveAndroidViaPlugin(body json.RawMessage) (*ResolvedAndroid, error) {
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
	paramsJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("substrate template resolve: marshal input: %w", err)
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: "local", Op: OpResolve, Params: json.RawMessage(paramsJSON)})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.JSON, nil
}

// resolveLocals / resolveAndroids project the whole opaque template map into resolved
// envelopes (a bad entry is skipped, cf. decodePluginKindMap).
func (uf *UnifiedFile) resolveLocals() map[string]*ResolvedLocal {
	if uf == nil || len(uf.Local) == 0 {
		return nil
	}
	out := make(map[string]*ResolvedLocal, len(uf.Local))
	for name, body := range uf.Local {
		r, err := resolveLocalViaPlugin(body)
		if err != nil || r == nil {
			continue
		}
		out[name] = r
	}
	return out
}

func (uf *UnifiedFile) resolveAndroids() map[string]*ResolvedAndroid {
	if uf == nil || len(uf.Android) == 0 {
		return nil
	}
	out := make(map[string]*ResolvedAndroid, len(uf.Android))
	for name, body := range uf.Android {
		r, err := resolveAndroidViaPlugin(body)
		if err != nil || r == nil {
			continue
		}
		out[name] = r
	}
	return out
}
