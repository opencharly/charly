// k8sgen.cue — the Kustomize-GENERATOR wire types shared between charly's core
// and the compiled-in candy/plugin-k8sgen (C8/M13; SDD conversion, per the
// standing operator directive: a hand-written wire struct not yet CUE-sourced is
// conversion-in-progress, never a sanctioned exception). These types live in
// package spec — the ONE importable home — because the consumers (candy/plugin-kube's
// materializeKustomize and candy/plugin-fleet's from-box path — the former in-core
// GenerateKubernetesKustomize shim is DELETED) AND the plugin (candy/plugin-k8sgen)
// construct and exchange them across the OpEmit Invoke boundary. The caller builds
// a KubernetesGenInput from KubernetesGenerateOpts, the plugin runs the pure generator
// (GenerateTree) and returns a KubernetesGenReply of RELATIVE-pathed manifest docs, and
// the caller does the disk I/O + the host-side egress gate (ValidateEgressValue)
// before the bytes hit disk. Plain structs — gengotypes generates them
// faithfully, no disjunction needed.

// #KubernetesGenInput is the pure-generation input the caller ships to plugin-k8sgen
// over OpEmit. Deploy is the deployment node (the former FleetNode =
// spec.Deploy); Cluster is the kind:kubernetes cluster template (the former KubernetesSpec =
// spec.Kubernetes); Ports / UID / GID are lifted from the image's OCI-label
// Capabilities host-side so the plugin needs no access to the package-main
// BoxMetadata type.
#KubernetesGenInput: {
	deployment_name!: string @go(DeploymentName)
	instance!:        string @go(Instance)
	image_ref!:       string @go(ImageRef)
	deploy!:          #Deploy @go(Deploy) // = the former FleetNode
	// cluster is the decoded kind:kubernetes cluster template. After the kubernetes
	// substrate-value de-type (Cutover K) the KERNEL no longer sets it — it
	// ships the opaque body in ClusterRaw and the plugin decodes ClusterRaw
	// into Cluster before generating, so the kernel never types spec.Kubernetes.
	cluster?:     #Kubernetes @go(Cluster)
	cluster_raw?: bytes @go(ClusterRaw,type=RawBody) // opaque kubernetes cluster body (Cutover K)
	ports!: [...string] @go(Ports) // from BoxMetadata.Port
	uid!:        int    @go(UID,type=int) // from BoxMetadata.UID
	gid!:        int    @go(GID,type=int) // from BoxMetadata.GID
	output_dir!: string @go(OutputDir)    // provenance; the host owns disk paths
}

// #KubernetesGenFile is one generated manifest the plugin returns: its RELATIVE path
// (under OutputDir/DeploymentName, e.g. "base/deployment.yaml"), the manifest
// as JSON (the host unmarshals it back to a value, egress-validates, and
// writes it as YAML), and the egress kind that gates it ("k8s_object" or
// "kustomization").
#KubernetesGenFile: {
	rel_path!:    string @go(RelPath)
	doc!:         bytes  @go(Doc,type=RawBody)
	egress_kind!: string @go(EgressKind)
}

// #KubernetesGenReply is the pure-generation output: the RELATIVE overlay path the
// host joins onto OutputDir/DeploymentName to form the `kubectl apply -k`
// argument, and the collected manifest files (base resources + base/overlay
// kustomizations).
#KubernetesGenReply: {
	overlay_rel_path!: string @go(OverlayRelPath)
	files!: [...#KubernetesGenFile] @go(Files)
}
