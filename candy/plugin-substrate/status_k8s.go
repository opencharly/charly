package substratekind

// status_k8s.go — the K8S substrate's OpStatus (K5: relocated verbatim from
// charly/status_collect_k8s.go). A `target: k8s` deploy does not run a
// container on this host — it emits a Kustomize manifest tree that
// `charly bundle sync` / `kubectl apply -k` applies to a remote cluster, so
// this collector reports GENERATION state (tree-present | not-generated) and
// the referenced cluster/context, never live pod health (that is a `kube:`
// check, candy/plugin-kube). Every input this needs (the folded project
// deploy tree, the kind:k8s template bodies) is fetched from the host via the
// established HostBuild("resolved-project") seam (already proven in
// production by candy/plugin-bundle's OpCompile) — resolving the referenced
// k8s template itself needs no cross-plugin hop at all: this SAME provider
// already implements the k8s substrate-template resolve (resolve.go), so it's
// an in-package call, not an Invoke.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// collectK8sStatus serves the k8s substrate's OpStatusCollect. It re-hydrates
// the resolved-project envelope over the reverse channel, enumerates every
// declared target:k8s deploy node, and emits one row per entry.
func collectK8sStatus(ctx context.Context, req spec.SubstrateStatusRequest) (spec.SubstrateStatusReply, error) {
	rp, err := fetchResolvedProject(ctx)
	if err != nil {
		return spec.SubstrateStatusReply{}, fmt.Errorf("k8s status-collect: %w", err)
	}

	entries := k8sDeployEntries(rp.Deploy)
	if len(entries) == 0 {
		return spec.SubstrateStatusReply{}, nil
	}
	treeRoot, rootErr := k8sTreeRoot()

	rows := make([]spec.DeploymentStatus, 0, len(entries))
	for _, name := range entries {
		node := rp.Deploy[name]
		row := spec.DeploymentStatus{
			Kind:      spec.SubstrateK8s,
			Source:    "tree",
			Image:     k8sImageRef(name, node),
			Container: name,
			RunMode:   req.RunMode,
		}

		treePresent := false
		if rootErr == nil {
			if _, statErr := os.Stat(filepath.Join(treeRoot, name)); statErr == nil {
				treePresent = true
			}
		}
		if treePresent {
			row.Status = "tree-present"
		} else {
			row.Status = "not-generated"
		}

		if ks := k8sSpecFor(rp.Templates, node); ks != nil && ks.KubeconfigContext != "" {
			row.Network = ks.KubeconfigContext
		} else if node != nil && node.From != "" {
			row.Network = node.From
		}

		rows = append(rows, row)
	}
	return spec.SubstrateStatusReply{Rows: rows}, nil
}

// fetchResolvedProject re-hydrates the resolved-project envelope over the
// established HostBuild("resolved-project") seam (candy/plugin-bundle's
// OpCompile proves this composition in production). Dir is left empty — a
// compiled-in substrate plugin shares the host process's cwd, so the host-side
// "resolved-project" handler's own os.Getwd() already resolves the right
// project without this plugin naming a directory.
func fetchResolvedProject(ctx context.Context) (*spec.ResolvedProject, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("reach host reverse channel: %w", err)
	}
	envReq, err := json.Marshal(spec.ResolvedProjectRequest{})
	if err != nil {
		return nil, fmt.Errorf("marshal resolved-project request: %w", err)
	}
	envJSON, err := exec.HostBuild(ctx, "resolved-project", envReq)
	if err != nil {
		return nil, fmt.Errorf("fetch resolved-project envelope: %w", err)
	}
	var rp spec.ResolvedProject
	if err := json.Unmarshal(envJSON, &rp); err != nil {
		return nil, fmt.Errorf("decode resolved-project envelope: %w", err)
	}
	return &rp, nil
}

// k8sTreeRoot returns <cwd>/.opencharly/k8s — the same canonical root
// defaultK8sOutputDir (charly/k8s_generate.go) emits Kustomize trees under.
// A separate implementation, not a shared call: the kernel/plugin boundary
// law forbids this plugin from importing charly core, so the two share only
// the convention, not the code. The compiled-in plugin shares the host's cwd.
func k8sTreeRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}

// k8sDeployEntries returns the names of every target:k8s deploy in the
// resolved-project's folded Deploy map, in deterministic (sorted) order.
func k8sDeployEntries(deploy map[string]*spec.Deploy) []string {
	if len(deploy) == 0 {
		return nil
	}
	var names []string
	for name, node := range deploy {
		if deploykit.ClassifyTarget(node) == "k8s" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// k8sImageRef resolves the image a k8s deploy runs, mirroring the k8s deploy
// preresolver: the node's explicit Box (carried on the wire as Image), falling
// back to the deploy name.
func k8sImageRef(name string, node *spec.Deploy) string {
	if node != nil && node.Image != "" {
		return node.Image
	}
	return name
}

// k8sSpecFor resolves the kind:k8s template referenced by node.From against
// the resolved-project's k8s template bodies. Nil when unreferenced or
// absent. Uses this SAME provider's own template-resolve leg (resolve.go) —
// an in-package call, never a cross-plugin Invoke.
func k8sSpecFor(templates *spec.ProjectTemplates, node *spec.Deploy) *spec.ResolvedK8s {
	if templates == nil || node == nil || node.From == "" {
		return nil
	}
	body, ok := templates.K8s[node.From]
	if !ok {
		return nil
	}
	out, err := resolveSubstrateTemplate(spec.SubstrateTemplateResolveRequest{K8s: &spec.K8sResolveInput{K8s: body}})
	if err != nil {
		return nil
	}
	var reply spec.K8sResolveReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return nil
	}
	return reply.Resolved
}
