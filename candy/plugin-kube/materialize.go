package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// materialize.go — the K5-A item-6 relocation: the Kustomize WRITE + egress-VALIDATE sequence
// that used to live host-side behind the former HostBuild seam
// (wrapping the disk-I/O body of the core
// GenerateKubernetesKustomize) now runs HERE. No host round trip is required: candy/plugin-k8sgen's
// GENERATOR is pure (no disk I/O, no egress — verb:k8sgen/OpEmit) and reachable peer-to-peer via
// exec.InvokeProvider; this plugin is a same-host subprocess with direct local disk access
// (plugin.go's own doc — same as its podman-storage access for image capabilities), so it does
// the MkdirAll/WriteFile/ReadFile itself; egress validation is likewise reachable peer-to-peer via
// exec.InvokeProvider(verb:egress, OpValidate) — the SAME resolve+Invoke shape
// candy/plugin-fleet/egress.go's ValidateEgressValue ran against the core-private registry, just
// reached through the executor instead.
//
// Two callers reach materializeKustomize: deploy:kubernetes's own OpPreresolve (preresolve.go, which
// already holds a live `exec` from its own Invoke) and, via the deploy:kubernetes OpEmit branch
// (provider.go/invokeKubernetesMaterialize), the host's source-less `charly fleet from-box
// --cluster <name>` path, which threads a throwaway kit.ShellExecutor{} purely
// to stand up the InvokeWithExecutor broker this plugin needs to reach k8sgen/egress — no venue
// carries any real meaning for this deploy-config-generation-only entry point (R3 dedup: ONE
// generate+write+validate implementation, not one host-side and one plugin-side copy).

// invokeKubernetesMaterialize serves Invoke(OpEmit) for deploy:kubernetes — the from-box entry point into
// materializeKustomize (dispatch lives in provider.go).
func invokeKubernetesMaterialize(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("deploy:kubernetes materialize: reach host reverse channel: %w", err)
	}
	var genReq spec.KubernetesGenerateKustomizeRequest
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &genReq); err != nil {
			return nil, fmt.Errorf("deploy:kubernetes materialize: decode request: %w", err)
		}
	}
	reply, err := materializeKustomize(ctx, exec, genReq)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("deploy:kubernetes materialize: marshal reply: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// materializeKustomize generates the pure Kustomize docs via verb:k8sgen's OpEmit,
// egress-validates + writes each file, copies any raw manifests verbatim, and returns the
// overlay path `kubectl apply -k` should target — the SAME contract
// the core GenerateKubernetesKustomize used to fulfil host-side (byte-identical output;
// see the K5-A item-6 spike report).
func materializeKustomize(ctx context.Context, exec *sdk.Executor, req spec.KubernetesGenerateKustomizeRequest) (spec.KubernetesGenerateKustomizeReply, error) {
	if req.Name == "" {
		return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: deployment name is required")
	}
	if len(req.CapsJSON) == 0 {
		return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: capabilities are required (read from OCI labels of %q)", req.ImageRef)
	}
	if len(req.ClusterJSON) == 0 {
		return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: cluster profile is required (kubernetes.cluster: not set?)")
	}

	var caps spec.BoxMetadata
	if err := json.Unmarshal(req.CapsJSON, &caps); err != nil {
		return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: decode capabilities: %w", err)
	}

	outputDir := req.OutputDir
	if outputDir == "" {
		var err error
		outputDir, err = defaultKubernetesOutputDir()
		if err != nil {
			return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: resolving default output dir: %w", err)
		}
	}

	var node spec.Deploy
	if req.Node != nil {
		node = *req.Node
	}

	// Build the pure-generation input (mirrors the deleted host-side GenerateKubernetesKustomize) and
	// Invoke verb:k8sgen peer-to-peer.
	input := spec.KubernetesGenInput{
		DeploymentName: req.Name,
		Instance:       "",
		ImageRef:       req.ImageRef,
		Deploy:         node,
		ClusterRaw:     req.ClusterJSON,
		Ports:          caps.Port,
		UID:            caps.UID,
		GID:            caps.GID,
		OutputDir:      outputDir,
	}
	params, err := json.Marshal(input)
	if err != nil {
		return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: marshal k8sgen input: %w", err)
	}
	resJSON, err := exec.InvokeProvider(ctx, "verb", "k8sgen", sdk.OpEmit, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: k8sgen invoke: %w", err)
	}
	var genReply spec.KubernetesGenReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &genReply); err != nil {
			return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: decode k8sgen reply: %w", err)
		}
	}

	root := filepath.Join(outputDir, req.Name)
	// Always (re)emit base from scratch — it's computed from inputs every time to avoid stale
	// artifacts. Overlays are additive (mirrors the deleted host-side body exactly).
	if err := os.RemoveAll(filepath.Join(root, "base")); err != nil {
		return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: cleaning base dir: %w", err)
	}

	for _, f := range genReply.Files {
		full := filepath.Join(root, f.RelPath)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return spec.KubernetesGenerateKustomizeReply{}, err
		}
		var doc any
		if err := json.Unmarshal(f.Doc, &doc); err != nil {
			return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: decoding generated %q: %w", f.RelPath, err)
		}
		if err := validateEgressValue(ctx, exec, f.EgressKind, f.RelPath, doc); err != nil {
			return spec.KubernetesGenerateKustomizeReply{}, err
		}
		if err := writeYAML(full, doc); err != nil {
			return spec.KubernetesGenerateKustomizeReply{}, err
		}
	}

	// Copy raw manifests from deployment.deploy.raw into base/raw/ verbatim (this plugin
	// already owns disk I/O for the generated files above; the generator registers their
	// kustomize resource paths but does no disk I/O itself).
	if node.Deploy != nil && len(node.Deploy.Raw) > 0 {
		rawDir := filepath.Join(root, "base", "raw")
		if err := os.MkdirAll(rawDir, 0755); err != nil {
			return spec.KubernetesGenerateKustomizeReply{}, err
		}
		for _, src := range node.Deploy.Raw {
			data, err := os.ReadFile(src)
			if err != nil {
				return spec.KubernetesGenerateKustomizeReply{}, fmt.Errorf("deploy:kubernetes materialize: reading raw manifest %q: %w", src, err)
			}
			if err := os.WriteFile(filepath.Join(rawDir, filepath.Base(src)), data, 0644); err != nil {
				return spec.KubernetesGenerateKustomizeReply{}, err
			}
		}
	}

	return spec.KubernetesGenerateKustomizeReply{
		OverlayPath: filepath.Join(root, genReply.OverlayRelPath),
		TreeRoot:    root,
	}, nil
}

// validateEgressValue is the peer-to-peer analogue of candy/plugin-fleet/egress.go's
// ValidateEgressValue: it
// marshals v to JSON and Invokes verb:egress's OpValidate through the executor (never the
// core-private registry, which this plugin cannot reach directly) — same params shape
// ({kind,label,mode,data}), same reply shape ({error}).
func validateEgressValue(ctx context.Context, exec *sdk.Executor, kind, label string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%s: egress marshal value: %w", label, err)
	}
	params, err := json.Marshal(map[string]string{"kind": kind, "label": label, "mode": "bytes", "data": string(data)})
	if err != nil {
		return fmt.Errorf("%s: egress marshal params: %w", label, err)
	}
	resJSON, err := exec.InvokeProvider(ctx, "verb", "egress", sdk.OpValidate, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return fmt.Errorf("%s: egress: %w", label, err)
	}
	var reply struct {
		Error string `json:"error"`
	}
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return fmt.Errorf("%s: egress: decode reply: %w", label, err)
		}
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}

func writeYAML(path string, doc any) error {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	return os.WriteFile(path, out, 0644)
}

// defaultKubernetesOutputDir resolves the canonical output directory for emitted kustomize trees —
// the plugin-side twin of the deleted core helper of the same name. This
// plugin runs as a same-host subprocess (LocalTransport) inheriting the host's cwd (plugin.go's
// own doc), so os.Getwd() resolves the SAME project directory the host would have.
func defaultKubernetesOutputDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}
