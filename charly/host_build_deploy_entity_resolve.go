package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/spec/spec"
)

// host_build_deploy_entity_resolve.go — the "deploy-entity-resolve" F10 host-builder (FINAL/K5
// unit 6a): the GENERIC LoadUnified-coupled lookup an F6 substrate preresolve body needs but
// cannot do itself (a separate-module plugin cannot import LoadUnified — a kernel Mechanism, R-E2
// stands: it never moves wholesale). `kind` is DATA end-to-end (W0): the tree-vs-template mode is
// routed by REQUEST SHAPE (tree_json set or not), never by inspecting kind's value, and a
// kind:<word> template lookup reaches the owning substrate plugin generically — no Go switch or
// per-kind map on kind anywhere in this file. Adding a fifth substrate needs no new case here,
// only a new word the owning plugin registers under class "kind".
const deployEntityResolveBuilderKind = "deploy-entity-resolve"

func hostBuildDeployEntityResolve(_ context.Context, req spec.DeployEntityResolveRequest, _ buildEngineContext) (spec.DeployEntityResolveReply, error) {
	dir := req.Dir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}

	if len(req.TreeJSON) > 0 {
		// The deploy-tree node lookup ("" / "deploy" / "bundle" callers — "bundle" is the
		// unit-6b-facing alias, candy/plugin-kube's k3s_post.go deployVMForwards, S3, resolving an
		// entityRef that may be a bundle key, needing the node's From field for one hop into a "vm"
		// lookup — same lookup as "deploy", no separate mechanism). The invoking plugin threads the
		// merged deploy tree it already resolved PLUGIN-SIDE (loaderkit.ResolveMergedTreeViaExecutor)
		// — this consumes it instead of re-loading the tree host-side (#55 Cone A Unit 3b
		// tree-threading, mirroring the deploy-del-resolve seam). Routed by tree_json's PRESENCE, not
		// req.Kind's value: every tree-node caller sets it, every kind:<word> template caller doesn't.
		var tree map[string]spec.BundleNode
		if err := json.Unmarshal(req.TreeJSON, &tree); err != nil {
			return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: decode threaded deploy tree: %w", err)
		}
		n, ok := tree[req.Name]
		if !ok {
			return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: no deploy entry %q", req.Name)
		}
		return spec.DeployEntityResolveReply{Node: &n}, nil
	}

	// kind:<word> template lookup (k8s/android/vm/local/pod) — genuinely kind-blind: the raw
	// template body is a DATA-indexed map lookup (ProjectTemplates.ByKind), and the owning
	// substrate plugin's existing OpResolve leg is reached by req.Kind as a registry key, never a Go
	// switch/map on the kind word.
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: loading charly.yml: %w", err)
	}
	if !ok || uf == nil {
		return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: no charly.yml or no kind:%s entities declared", req.Kind)
	}
	body := uf.ProjectTemplates().ByKind(req.Kind)[req.Name]
	if len(body) == 0 {
		return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: no kind:%s entity named %q", req.Kind, req.Name)
	}
	entityJSON, err := resolveEntityTemplate(req.Kind, body)
	if err != nil {
		return spec.DeployEntityResolveReply{}, err
	}
	if len(entityJSON) == 0 {
		return spec.DeployEntityResolveReply{}, fmt.Errorf("deploy-entity-resolve: kind:%s entity %q resolved to an empty value", req.Kind, req.Name)
	}
	return spec.DeployEntityResolveReply{EntityJSON: entityJSON}, nil
}

// resolveEntityTemplate projects one opaque kind:<word> template body into its Resolved* envelope
// JSON, via the owning substrate plugin's existing OpResolve leg. `kind` flows through as DATA —
// both the registry lookup (providerRegistry.ResolveKind) and the discriminated OpResolve request
// shape ({"<kind>":{"<kind>":body}}, identical across every substrate word — see
// spec.SubstrateTemplateResolveRequest) are keyed by the string value, never branched on in Go.
// Every substrate word's OpResolve reply shares the same JSON shape too ({"resolved": …} — see
// spec.K8sResolveReply/VmResolveReply/AndroidResolveReply/LocalResolveReply/PodResolveReply), so
// the "resolved" field is extracted generically, without decoding into any concrete kind type
// (TestNoConcreteKindInKernel).
func resolveEntityTemplate(kind string, body json.RawMessage) (json.RawMessage, error) {
	prov, ok := providerRegistry.ResolveKind(kind)
	if !ok {
		return nil, fmt.Errorf("deploy-entity-resolve: kind provider %q not registered", kind)
	}
	reqJSON, err := json.Marshal(map[string]map[string]json.RawMessage{kind: {kind: body}})
	if err != nil {
		return nil, fmt.Errorf("deploy-entity-resolve: encode kind:%s template: %w", kind, err)
	}
	out, err := invokeTyped[json.RawMessage, json.RawMessage](context.Background(), prov, kind, OpResolve, reqJSON)
	if err != nil {
		return nil, fmt.Errorf("deploy-entity-resolve: resolve kind:%s: %w", kind, err)
	}
	var reply struct {
		Resolved json.RawMessage `json:"resolved"`
	}
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return nil, fmt.Errorf("deploy-entity-resolve: decode kind:%s resolve reply: %w", kind, err)
		}
	}
	return reply.Resolved, nil
}

var _ = func() bool {
	registerHostBuilder(deployEntityResolveBuilderKind, typedHostBuilder(deployEntityResolveBuilderKind, hostBuildDeployEntityResolve))
	return true
}()
