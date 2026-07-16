package main

// k8s_deploy_preresolve.go — the HOST-SIDE deploy:k8s preresolver (F1).
//
// `target: k8s` is an EXTERNAL deploy substrate served out-of-process by
// candy/plugin-kube (the same plugin that serves the `kube:` check verb + the
// k3s kubeconfig-merge seam — one plugin owns ALL k8s cluster interaction, so the
// client-go dep + the kubectl-apply path stay the single copy, R3). The plugin
// drives the cluster (`kubectl apply -k`) but cannot GENERATE the Kustomize tree.
// The GENERATOR lives in the compiled-in candy/plugin-k8sgen (verb:k8sgen, C8/M13),
// fronted by the in-core GenerateK8sKustomize shim: the shim lifts the image
// Capabilities (read from the OCI labels via ExtractMetadata) to ports/uid/gid,
// Invokes the generator's OpEmit, then applies the host-side egress gate (#K8sObject
// / #Kustomization) + disk I/O. GenerateK8sKustomize has a SECOND consumer
// (`charly bundle from-box --target k8s`). So this preresolver does the host half —
// resolve the cluster template + image Capabilities, GENERATE the egress-validated
// Kustomize tree, and ship its overlay path in DeployVenue.Substrate (a
// spec.K8sDeployVenue). The plugin then runs `kubectl apply -k <overlay>` and
// returns the teardown ops the host records.
//
// The plugin runs as a HOST subprocess (LocalTransport), so it reads the generated
// tree on disk + runs the host's kubectl against the merged kubeconfig directly —
// it never needs the executor reverse channel for k8s (like deploy:android).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// register the k8s deploy preresolver at package-var init (before any init()),
// race-free with the rest of the F1 wiring.
var _ = func() bool {
	registerDeployPreresolver("k8s", k8sDeployPreresolve)
	return true
}()

// k8sDeployPreresolve is the deploy:k8s preresolver. It resolves the kind:k8s
// cluster template + the image Capabilities, GENERATES the egress-validated
// Kustomize tree (GenerateK8sKustomize — the SAME generator `charly bundle
// from-box --target k8s` uses, R3), and marshals a spec.K8sDeployVenue carrying the
// overlay path. node may be nil (the Update path carries no DeployContext) — it
// then re-resolves the deploy node from the tree by name. plans is unused: k8s does
// NOT consume the InstallPlan IR (GenerateK8sKustomize reads caps + node + cluster).
func k8sDeployPreresolve(name, dir string, node *BundleNode, _ []*InstallPlan) (json.RawMessage, error) {
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	if node == nil {
		tree, err := resolveTreeRoot(dir)
		if err != nil {
			return nil, fmt.Errorf("deploy %q: resolve k8s deploy node: %w", name, err)
		}
		n, ok := tree[name]
		if !ok {
			return nil, fmt.Errorf("deploy %q: no k8s deploy entry", name)
		}
		node = &n
	}

	// Resolve the kind:k8s cluster template (node.From → the K8sSpec).
	clusterName := node.From
	if clusterName == "" {
		return nil, fmt.Errorf("deploy %q: target=k8s requires `k8s:` (kind:k8s cluster reference) on the deployment entry", name)
	}
	cluster := findK8sSpec(dir, clusterName)
	if cluster == nil {
		return nil, fmt.Errorf("deploy %q: cluster %q not declared in the k8s: section", name, clusterName)
	}

	// Resolve image + capabilities. node.Image is the authored ref (a short box
	// name, or a namespace-qualified `fedora.web`); resolve it to the fully-qualified
	// LOCAL image ref the same way the pod-deploy path does (leafName drops the
	// namespace, resolveLocalImageRef label-matches the local build) so BOTH
	// ExtractMetadata AND the generated Deployment's container image name the real
	// pulled/built image — never the bare authored ref ExtractMetadata can't find.
	rt, err := ResolveRuntime()
	if err != nil {
		return nil, fmt.Errorf("deploy %q: resolving runtime: %w", name, err)
	}
	authored := node.Image
	if authored == "" {
		authored = name
	}
	// Honor a pinned node.Version (the bed-scoped per-run tag #75, or an operator
	// version pin): use the EXACT <name>:<version> local ref instead of the
	// store-global short-name→newest-local-CalVer resolution, so concurrent beds
	// building the same fixture image name never pick each other's build. Mirrors
	// build_overlay.go's `tag != ""` branch (R3).
	var imageRef string
	if node.Version != "" {
		imageRef = leafName(authored) + ":" + node.Version
		if !LocalImageExists(rt.RunEngine, imageRef) {
			return nil, fmt.Errorf("deploy %q: pinned image %q not present in local %s storage", name, imageRef, rt.RunEngine)
		}
	} else {
		resolved, rerr := kit.ResolveLocalImageRef(rt.RunEngine, leafName(authored))
		if rerr != nil {
			return nil, fmt.Errorf("deploy %q: resolving image %q: %w", name, authored, rerr)
		}
		imageRef = resolved
	}
	caps, err := ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: extracting capabilities from image %q: %w", name, imageRef, err)
	}
	if caps == nil {
		return nil, fmt.Errorf("deploy %q: image %q has no ai.opencharly labels (not an opencharly image?)", name, imageRef)
	}

	// GENERATE the egress-validated Kustomize tree under .opencharly/k8s/<name>/.
	outDir, err := defaultK8sOutputDir()
	if err != nil {
		return nil, err
	}
	overlayPath, err := GenerateK8sKustomize(K8sGenerateOpts{
		DeploymentName: name,
		ImageRef:       imageRef,
		Deploy:         *node,
		Capabilities:   caps,
		Cluster:        cluster,
		OutputDir:      outDir,
	})
	if err != nil {
		return nil, fmt.Errorf("deploy %q: generating kustomize: %w", name, err)
	}

	venue := spec.K8sDeployVenue{
		OverlayPath: overlayPath,
		TreeRoot:    filepath.Join(outDir, name),
		KubeContext: cluster.KubeconfigContext, // `kubectl --context` so apply targets THIS cluster, not the current-context
		DeployName:  name,
	}
	payload, err := json.Marshal(venue)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: marshal k8s venue: %w", name, err)
	}
	return payload, nil
}
