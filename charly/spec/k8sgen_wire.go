package spec

import "encoding/json"

// k8sgen_wire.go — the Kustomize-GENERATOR wire types shared between charly's
// core (package main) and the compiled-in candy/plugin-k8sgen (C8/M13).
//
// These types live in package spec — the ONE importable home — because BOTH the
// host (the in-core GenerateK8sKustomize shim, k8s_generate.go) AND the plugin
// (candy/plugin-k8sgen, via the replace → ../../charly module edge) construct and
// exchange them across the OpEmit Invoke boundary. The host builds a K8sGenInput
// from K8sGenerateOpts (extracting Ports/UID/GID from the image Capabilities),
// the plugin runs the pure generator (GenerateTree) and returns a K8sGenReply of
// RELATIVE-pathed manifest docs, and the host does the disk I/O + the host-side
// egress gate (ValidateEgressValue) before the bytes hit disk. There is NO
// duplicate type for any of these concepts (R3).

// K8sGenInput is the pure-generation input the host ships to plugin-k8sgen over
// OpEmit. Deploy is the deployment node (the former BundleNode = spec.Deploy);
// Cluster is the kind:k8s cluster template (the former K8sSpec = spec.K8s); Ports
// / UID / GID are lifted from the image's OCI-label Capabilities host-side so the
// plugin needs no access to the package-main BoxMetadata type.
type K8sGenInput struct {
	DeploymentName string   `json:"deployment_name"`
	Instance       string   `json:"instance"`
	ImageRef       string   `json:"image_ref"`
	Deploy         Deploy   `json:"deploy"`     // = the former BundleNode
	Cluster        K8s      `json:"cluster"`    // = the former K8sSpec
	Ports          []string `json:"ports"`      // from BoxMetadata.Port
	UID            int      `json:"uid"`        // from BoxMetadata.UID
	GID            int      `json:"gid"`        // from BoxMetadata.GID
	OutputDir      string   `json:"output_dir"` // provenance; the host owns disk paths
}

// K8sGenFile is one generated manifest the plugin returns: its RELATIVE path
// (under OutputDir/DeploymentName, e.g. "base/deployment.yaml"), the manifest as
// JSON (the host unmarshals it back to a value, egress-validates, and writes it as
// YAML), and the egress kind that gates it ("k8s_object" or "kustomization").
type K8sGenFile struct {
	RelPath    string          `json:"rel_path"`
	Doc        json.RawMessage `json:"doc"`
	EgressKind string          `json:"egress_kind"`
}

// K8sGenReply is the pure-generation output: the RELATIVE overlay path the host
// joins onto OutputDir/DeploymentName to form the `kubectl apply -k` argument, and
// the collected manifest files (base resources + base/overlay kustomizations).
type K8sGenReply struct {
	OverlayRelPath string       `json:"overlay_rel_path"`
	Files          []K8sGenFile `json:"files"`
}
