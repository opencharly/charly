package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// preresolve.go — the `deploy:kubernetes` PRERESOLVE leg (F6, FINAL/K5 unit 6a): relocated from
// the former core preresolver. Resolves the kind:kubernetes cluster template + the image
// Capabilities, GENERATES the egress-validated Kustomize tree, and returns a
// spec.KubernetesDeployVenue carrying the overlay path — the SAME payload the host used to build
// directly, now assembled here. The image-ref + capabilities resolution is pure sdk/kit +
// sdk/deploykit (this plugin runs as a host subprocess with direct local podman storage
// access, per plugin.go's own doc). The cluster/node lookup self-loads the project PLUGIN-SIDE
// now (K-wave W3a A3-phase-2: loaderkit.ResolveMergedTreeViaExecutor /
// ResolveKubernetesEntityViaExecutor, unblocked by W1's LoadUnifiedViaExecutor) — the former
// "deploy-entity-resolve" HostBuild seam this round-tripped through is DELETED; the
// egress-gated Kustomize GENERATION itself is done ENTIRELY here too (materialize.go, K5-A item
// 6 — verb:k8sgen/verb:egress reached peer-to-peer via InvokeProvider, disk I/O done directly by
// this plugin) — no host round trip anywhere in this leg anymore. The from-box source-less path
// (`charly fleet from-box --cluster <name>`, candy/plugin-fleet/deploy_from_box.go) reaches this
// SAME materializeKustomize via a dedicated OpEmit dispatch (provider.go), R3 dedup.

// kubernetesPreresolveParams decodes the host's marshalDeployOpParams envelope (name/dir/node/plans —
// the SAME ad-hoc shape every OpPreresolve dispatch carries; kubernetes does not consume plans).
type kubernetesPreresolveParams struct {
	Name string       `json:"name"`
	Dir  string       `json:"dir"`
	Node *spec.Deploy `json:"node"`
}

// invokeKubernetesPreresolve serves Invoke(OpPreresolve) for deploy:kubernetes.
func invokeKubernetesPreresolve(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("deploy:kubernetes preresolve: reach host reverse channel: %w", err)
	}
	var p kubernetesPreresolveParams
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &p); err != nil {
			return nil, fmt.Errorf("deploy:kubernetes preresolve: decode params: %w", err)
		}
	}

	node := p.Node
	if node == nil {
		// Resolve the merged deploy tree PLUGIN-SIDE (K-wave W3a A3-phase-2: the former
		// "deploy-entity-resolve" TreeJSON round-trip was dead weight — the tree is already a live
		// Go value here, so this is a direct map lookup, not a host seam call). The enclosing
		// OpDeployDispatch already connected the deployment's plugins (command:fleet's
		// resolveTreeViaLoader), so this reuses that connect (no re-dial mid-Invoke).
		tree, terr := loaderkit.ResolveMergedTreeViaExecutor(ctx, exec, p.Dir)
		if terr != nil {
			return nil, fmt.Errorf("deploy:kubernetes preresolve: resolve deploy tree: %w", terr)
		}
		n, ok := tree[p.Name]
		if !ok {
			return nil, fmt.Errorf("deploy:kubernetes preresolve: resolve deploy %q: no deploy entry %q", p.Name, p.Name)
		}
		node = &n
	}
	clusterName := ""
	if node != nil {
		clusterName = node.From
	}
	if clusterName == "" {
		return nil, fmt.Errorf("deploy %q: target=kubernetes requires `kubernetes:` (kind:kubernetes cluster reference) on the deployment entry", p.Name)
	}

	// K-wave W3a A3-phase-2: self-load the kind:kubernetes entity plugin-side
	// (loaderkit.ResolveKubernetesEntityViaExecutor) instead of the deleted "deploy-entity-resolve" host
	// seam — unblocked now that LoadUnifiedViaExecutor (W1) lets a plugin load the project itself.
	cluster, err := loaderkit.ResolveKubernetesEntityViaExecutor(ctx, exec, p.Dir, clusterName)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: resolving cluster %q: %w", p.Name, clusterName, err)
	}
	if cluster == nil {
		return nil, fmt.Errorf("deploy %q: kind:kubernetes cluster %q resolved to an empty value", p.Name, clusterName)
	}
	clusterJSON, err := json.Marshal(cluster)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: marshal resolved cluster %q: %w", p.Name, clusterName, err)
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
	// access; the former HostBuild seam is retired).
	genReply, err := materializeKustomize(ctx, exec, spec.KubernetesGenerateKustomizeRequest{
		Name:        p.Name,
		ImageRef:    imageRef,
		Node:        node,
		CapsJSON:    capsJSON,
		ClusterJSON: clusterJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("deploy %q: generating kustomize: %w", p.Name, err)
	}

	venue := spec.KubernetesDeployVenue{
		OverlayPath: genReply.OverlayPath,
		TreeRoot:    filepath.Clean(genReply.TreeRoot),
		KubeContext: cluster.KubeconfigContext,
		DeployName:  p.Name,
	}
	out, err := json.Marshal(venue)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: marshal kubernetes venue: %w", p.Name, err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// hostProjectDir resolves the project directory via the "deploy-plugins-connect" host seam — the
// SAME preamble command:fleet's resolveTreeViaLoader runs (it returns os.Getwd() host-side + connects
// the deployment's plugins). Used by a leg that has no dispatch-threaded p.Dir of its own (the
// post-provision k3s hint handler, k3s_post.go's deployVMForwards) to feed the plugin-side
// self-load helpers (loaderkit.ResolveMergedTreeViaExecutor / Resolve{Kubernetes,Vm}EntityViaExecutor).
func hostProjectDir(ctx context.Context, exec *sdk.Executor, deployName string) (string, error) {
	reqJSON, err := json.Marshal(spec.DeployPluginsConnectRequest{Path: deployName})
	if err != nil {
		return "", err
	}
	resJSON, err := exec.HostBuild(ctx, "deploy-plugins-connect", reqJSON)
	if err != nil {
		return "", err
	}
	var reply spec.DeployPluginsConnectReply
	if err := json.Unmarshal(resJSON, &reply); err != nil {
		return "", err
	}
	return reply.Dir, nil
}
