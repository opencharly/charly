// CUE schema for the `k8s` kind. #K8s validates ONE value of the `k8s:` map
// (K8sSpec; absorbed the former ClusterProfile). CLOSED — every K8sSpec field is
// modeled, so an unknown key is a typo. The documented enum domains + sub-object
// shapes are constrained. ONE exception: `pod_default.tolerations` stays OPEN —
// it is a genuine passthrough of raw Kubernetes Toleration objects
// ([]map[string]any). Plural field names that mirror Kubernetes output keys are
// preserved verbatim. Shared #Step from _common.cue.

#K8s: {
	// May be empty (a cluster-policy-only template runs no workload itself).
	box: string

	replica?:   int & >=0 @go(,type=*int)
	resources?: #K8sResources @go(Resources,optional=nillable)
	hostnames?: [...#K8sHostname]

	kubeconfig_context?: string                                                      @go(KubeconfigContext)
	admission_policy?:   "restricted" | "baseline" | "privileged"                    @go(AdmissionPolicy)
	default_namespace?:  *"default" | (string & =~"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$") @go(DefaultNamespace)

	storage?:        #K8sStorage
	ingress?:        #K8sIngressDefaults
	gateway_api?:    #K8sGatewayAPI @go(GatewayAPI)
	secret?:         #K8sSecretsBackend
	image_default?:  #K8sImagesDefaults @go(ImageDefault)
	pod_default?:    #K8sPodDefaults    @go(PodDefault)
	observability?:  #K8sObservability
	network_policy?: "auto" | "strict" | "none" @go(NetworkPolicy)
	defaults?:       #K8sResourceDefaults

	plan?: [...#Step]
}

#K8sResources: {
	requests?: #K8sResourceValues
	limits?:   #K8sResourceValues
}
#K8sResourceValues: {
	cpu?:    string & =~"^[0-9]+(\\.[0-9]+)?m?$" @go(CPU)
	memory?: string & =~"^[0-9]+(\\.[0-9]+)?(Ei|Pi|Ti|Gi|Mi|Ki|E|P|T|G|M|k|m)?$"
}
#K8sHostname: {
	host:  string & =~"^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$"
	tls?:  bool @go(TLS)
	path?: string & =~"^/"
}
#K8sStorage: {
	class_default?:       string @go(ClassDefault)
	class_cheap?:         string @go(ClassCheap)
	class_encrypted?:     string @go(ClassEncrypted)
	class_fast?:          string @go(ClassFast)
	access_mode_default?: ("ReadWriteOnce" | "ReadWriteMany" | "ReadOnlyMany" | "ReadWriteOncePod") @go(AccessModeDefault)
}
#K8sIngressDefaults: {
	enabled?:           bool
	class?:             string
	cert_issuer?:       string                                                 @go(CertIssuer)
	path_type_default?: ("Prefix" | "Exact" | "ImplementationSpecific") @go(PathTypeDefault)
}
#K8sGatewayAPI: {
	enabled?:       bool
	gateway_class?: string @go(GatewayClass)
}
#K8sSecretsBackend: {
	backend?: "external-secrets" | "sealed-secrets" | "raw"
	store?:   string
	prefix?:  string
}
#K8sImagesDefaults: {
	pull_policy?: ("IfNotPresent" | "Always" | "Never") @go(PullPolicy)
	pull_secrets?: [...string] @go(PullSecrets)
}
#K8sPodDefaults: {
	priority_class?: string @go(PriorityClass)
	// Raw Kubernetes Toleration objects (Go []map[string]any) — genuine
	// passthrough, so each element stays OPEN.
	tolerations?: [...{...}]
	node_selector?: {[string]: string} @go(NodeSelector)
}
#K8sObservability: {
	service_monitor?:          bool   @go(ServiceMonitor)
	service_monitor_interval?: string @go(ServiceMonitorInterval)
}
#K8sResourceDefaults: {
	labels?: [string]:      string
	annotations?: [string]: string
}
