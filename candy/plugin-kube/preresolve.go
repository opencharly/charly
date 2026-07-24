package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// preresolve.go — the `deploy:k8s` PRERESOLVE leg (F6, FINAL/K5 unit 6a): relocated from
// charly/k8s_deploy_preresolve.go. Resolves the kind:k8s cluster template + the image
// Capabilities, GENERATES the egress-validated Kustomize tree, and returns a
// spec.K8sDeployVenue carrying the overlay path — the SAME payload the host used to build
// directly, now assembled here. The image-ref + capabilities resolution is pure sdk/kit +
// sdk/deploykit (this plugin runs as a host subprocess with direct local podman storage
// access, per plugin.go's own doc). Only the LoadUnified-coupled cluster/node lookup (the
// "deploy-entity-resolve" HostBuild seam) reaches the host; the egress-gated Kustomize
// GENERATION itself is done ENTIRELY here (materialize.go, K5-A item 6 — verb:k8sgen/verb:egress
// reached peer-to-peer via InvokeProvider, disk I/O done directly by this plugin) — no host round
// trip. The from-box source-less path (`charly bundle from-box --target k8s`,
// charly/k8s_deploy_from_box.go) reaches this SAME materializeKustomize via a dedicated OpEmit
// dispatch (provider.go), R3 dedup.

// k8sPreresolveParams decodes the host's marshalDeployOpParams envelope (name/dir/node/plans —
// the SAME ad-hoc shape every OpPreresolve dispatch carries; k8s does not consume plans).
type k8sPreresolveParams struct {
	Name string       `json:"name"`
	Dir  string       `json:"dir"`
	Node *spec.Deploy `json:"node"`
}

// invokeK8sPreresolve serves Invoke(OpPreresolve) for deploy:k8s.
func invokeK8sPreresolve(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("deploy:k8s preresolve: reach host reverse channel: %w", err)
	}
	var p k8sPreresolveParams
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &p); err != nil {
			return nil, fmt.Errorf("deploy:k8s preresolve: decode params: %w", err)
		}
	}

	node := p.Node
	if node == nil {
		var reply spec.DeployEntityResolveReply
		if err := k8sEntityResolve(ctx, exec, spec.DeployEntityResolveRequest{Kind: "deploy", Name: p.Name, Dir: p.Dir}, &reply); err != nil {
			return nil, fmt.Errorf("deploy:k8s preresolve: resolve deploy %q: %w", p.Name, err)
		}
		node = reply.Node
	}
	clusterName := ""
	if node != nil {
		clusterName = node.From
	}
	if clusterName == "" {
		return nil, fmt.Errorf("deploy %q: target=k8s requires `k8s:` (kind:k8s cluster reference) on the deployment entry", p.Name)
	}

	var clusterReply spec.DeployEntityResolveReply
	if err := k8sEntityResolve(ctx, exec, spec.DeployEntityResolveRequest{Kind: "k8s", Name: clusterName, Dir: p.Dir}, &clusterReply); err != nil {
		return nil, fmt.Errorf("deploy %q: resolving cluster %q: %w", p.Name, clusterName, err)
	}

	// Resolve image + capabilities — pure sdk/kit + sdk/deploykit, no LoadUnified needed (this
	// plugin runs as a host subprocess with direct local podman storage access).
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return nil, fmt.Errorf("deploy %q: resolving runtime: %w", p.Name, err)
	}
	authored := ""
	if node != nil {
		authored = node.Image
	}
	if authored == "" {
		authored = p.Name
	}
	var imageRef string
	if node != nil && node.Version != "" {
		imageRef = spec.LeafName(authored) + ":" + node.Version
		if !kit.LocalImageExists(rt.RunEngine, imageRef) {
			return nil, fmt.Errorf("deploy %q: pinned image %q not present in local %s storage", p.Name, imageRef, rt.RunEngine)
		}
	} else {
		resolved, rerr := kit.ResolveLocalImageRef(rt.RunEngine, spec.LeafName(authored))
		if rerr != nil {
			return nil, fmt.Errorf("deploy %q: resolving image %q: %w", p.Name, authored, rerr)
		}
		imageRef = resolved
	}
	caps, err := deploykit.ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: extracting capabilities from image %q: %w", p.Name, imageRef, err)
	}
	if caps == nil {
		return nil, fmt.Errorf("deploy %q: image %q has no ai.opencharly labels (not an opencharly image?)", p.Name, imageRef)
	}
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: marshal capabilities: %w", p.Name, err)
	}

	// Generate the egress-validated Kustomize tree DIRECTLY (K5-A item 6 — no host round trip:
	// materializeKustomize Invokes verb:k8sgen + verb:egress peer-to-peer via this SAME `exec`
	// and does its own disk I/O, since this plugin is a same-host subprocess with direct disk
	// access; the former "k8s-generate-kustomize" HostBuild seam is retired).
	genReply, err := materializeKustomize(ctx, exec, spec.K8sGenerateKustomizeRequest{
		Name:        p.Name,
		ImageRef:    imageRef,
		Node:        node,
		CapsJSON:    capsJSON,
		ClusterJSON: clusterReply.EntityJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("deploy %q: generating kustomize: %w", p.Name, err)
	}

	var cluster resolvedK8sView
	if err := json.Unmarshal(clusterReply.EntityJSON, &cluster); err != nil {
		return nil, fmt.Errorf("deploy %q: decode cluster view: %w", p.Name, err)
	}

	venue := spec.K8sDeployVenue{
		OverlayPath: genReply.OverlayPath,
		TreeRoot:    filepath.Clean(genReply.TreeRoot),
		KubeContext: cluster.KubeconfigContext,
		DeployName:  p.Name,
	}
	out, err := json.Marshal(venue)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: marshal k8s venue: %w", p.Name, err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// resolvedK8sView is the narrow subset of the opaque ResolvedK8s entity this preresolve body
// needs (just KubeconfigContext, for the returned K8sDeployVenue) — decoded from the SAME
// "deploy-entity-resolve" kind="k8s" reply materializeKustomize ALSO receives (as opaque
// ClusterJSON, via ClusterRaw), so both consumers read the identical host-resolved cluster spec.
type resolvedK8sView struct {
	KubeconfigContext string `json:"kubeconfig_context"`
}

// k8sEntityResolve Invokes the "deploy-entity-resolve" HostBuild seam and decodes the reply.
func k8sEntityResolve(ctx context.Context, exec *sdk.Executor, req spec.DeployEntityResolveRequest, out *spec.DeployEntityResolveReply) error {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return err
	}
	resJSON, err := exec.HostBuild(ctx, "deploy-entity-resolve", reqJSON)
	if err != nil {
		return err
	}
	return json.Unmarshal(resJSON, out)
}
