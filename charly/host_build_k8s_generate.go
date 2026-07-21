package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/opencharly/sdk/spec"
)

// host_build_k8s_generate.go — the "k8s-generate-kustomize" F10 host-builder (FINAL/K5 unit
// 6a): wraps k8s_generate.go's GenerateK8sKustomize (compiled-in verb:k8sgen Invoke + the M16
// egress gate + disk I/O — all core-only glue that STAYS in charly/, unchanged) so the
// (now plugin-side) deploy:k8s preresolve body can reach it. k8s_deploy_from_box.go's
// `charly bundle from-box --target k8s` keeps calling GenerateK8sKustomize directly — this seam
// is an ADDITIONAL caller, not a replacement.
const k8sGenerateKustomizeBuilderKind = "k8s-generate-kustomize"

func hostBuildK8sGenerateKustomize(_ context.Context, req spec.K8sGenerateKustomizeRequest, _ buildEngineContext) (spec.K8sGenerateKustomizeReply, error) {
	var caps Capabilities
	if err := json.Unmarshal(req.CapsJSON, &caps); err != nil {
		return spec.K8sGenerateKustomizeReply{}, fmt.Errorf("k8s-generate-kustomize: decode capabilities: %w", err)
	}
	var cluster ResolvedK8s
	if err := json.Unmarshal(req.ClusterJSON, &cluster); err != nil {
		return spec.K8sGenerateKustomizeReply{}, fmt.Errorf("k8s-generate-kustomize: decode cluster: %w", err)
	}
	outputDir := req.OutputDir
	if outputDir == "" {
		var err error
		outputDir, err = defaultK8sOutputDir()
		if err != nil {
			return spec.K8sGenerateKustomizeReply{}, fmt.Errorf("k8s-generate-kustomize: resolving default output dir: %w", err)
		}
	}
	node := spec.BundleNode{}
	if req.Node != nil {
		node = *req.Node
	}
	overlayPath, err := GenerateK8sKustomize(K8sGenerateOpts{
		DeploymentName: req.Name,
		ImageRef:       req.ImageRef,
		Deploy:         node,
		Capabilities:   &caps,
		Cluster:        &cluster,
		OutputDir:      outputDir,
	})
	if err != nil {
		return spec.K8sGenerateKustomizeReply{}, err
	}
	// Mirrors the deleted k8s_deploy_preresolve.go's TreeRoot computation exactly.
	treeRoot := filepath.Join(outputDir, req.Name)
	return spec.K8sGenerateKustomizeReply{OverlayPath: overlayPath, TreeRoot: treeRoot}, nil
}

var _ = func() bool {
	registerHostBuilder(k8sGenerateKustomizeBuilderKind, typedHostBuilder(k8sGenerateKustomizeBuilderKind, hostBuildK8sGenerateKustomize))
	return true
}()
