package main

import (
	"os"
	"path/filepath"
)

// bundle_add_cmd_k8s.go — shared k8s-target helper(s).
//
// `target: k8s` is an EXTERNAL deploy substrate (F1): the host-side preresolver
// (k8s_deploy_preresolve.go) calls the output-dir resolver below + GenerateK8sKustomize
// to emit the egress-validated Kustomize tree, and candy/plugin-kube runs `kubectl
// apply -k` / `kubectl delete -k` over the external-deploy reverse channel. K8s
// doesn't consume the InstallPlan IR — GenerateK8sKustomize is an in-core shim that
// lifts the image Capabilities to ports/uid/gid, Invokes the compiled-in
// candy/plugin-k8sgen generator (verb:k8sgen, C8/M13), then does the host-side
// egress gate + disk I/O. The source-less `charly bundle from-box --target k8s` path
// (k8s_deploy_from_box.go) calls the SAME resolver + shim (R3).

// defaultK8sOutputDir resolves the canonical output directory for
// emitted kustomize trees. Mirrors DeployFromBox's default.
func defaultK8sOutputDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}
