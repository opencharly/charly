// CUE schema for the `kubernetes` kind. #Kubernetes validates ONE value of the
// `kubernetes:` map (KubernetesSpec; absorbed the former ClusterProfile). CLOSED —
// every KubernetesSpec field is
// modeled, so an unknown key is a typo. The documented enum domains + sub-object
// shapes are constrained. ONE exception: `pod_default.tolerations` stays OPEN —
// it is a genuine passthrough of raw Kubernetes Toleration objects
// ([]map[string]any). Plural field names that mirror Kubernetes output keys are
// preserved verbatim. Shared #Step from _common.cue.

#Kubernetes: {
	// May be empty (a cluster-policy-only template runs no workload itself).
	box: string

	replica?:   int & >=0 @go(,type=*int)
	resources?: #KubernetesResources @go(Resources,optional=nillable)
	hostnames?: [...#KubernetesHostname]

	kubeconfig_context?: string                                                      @go(KubeconfigContext)
	admission_policy?:   "restricted" | "baseline" | "privileged"                    @go(AdmissionPolicy)
	default_namespace?:  *"default" | (string & =~"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$") @go(DefaultNamespace)

	storage?:        #KubernetesStorage
	ingress?:        #KubernetesIngressDefaults
	gateway_api?:    #KubernetesGatewayAPI @go(GatewayAPI)
	secret?:         #KubernetesSecretsBackend
	image_default?:  #KubernetesImagesDefaults @go(ImageDefault)
	pod_default?:    #KubernetesPodDefaults    @go(PodDefault)
	observability?:  #KubernetesObservability
	network_policy?: "auto" | "strict" | "none" @go(NetworkPolicy)
	defaults?:       #KubernetesResourceDefaults

	plan?: [...#Step]
}

#KubernetesResources: {
	requests?: #KubernetesResourceValues
	limits?:   #KubernetesResourceValues
}
#KubernetesResourceValues: {
	cpu?:    string & =~"^[0-9]+(\\.[0-9]+)?m?$" @go(CPU)
	memory?: string & =~"^[0-9]+(\\.[0-9]+)?(Ei|Pi|Ti|Gi|Mi|Ki|E|P|T|G|M|k|m)?$"
}
#KubernetesHostname: {
	host:  string & =~"^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$"
	tls?:  bool @go(TLS)
	path?: string & =~"^/"
}
#KubernetesStorage: {
	class_default?:       string @go(ClassDefault)
	class_cheap?:         string @go(ClassCheap)
	class_encrypted?:     string @go(ClassEncrypted)
	class_fast?:          string @go(ClassFast)
	access_mode_default?: ("ReadWriteOnce" | "ReadWriteMany" | "ReadOnlyMany" | "ReadWriteOncePod") @go(AccessModeDefault)
}
#KubernetesIngressDefaults: {
	enabled?:           bool
	class?:             string
	cert_issuer?:       string                                                 @go(CertIssuer)
	path_type_default?: ("Prefix" | "Exact" | "ImplementationSpecific") @go(PathTypeDefault)
}
#KubernetesGatewayAPI: {
	enabled?:       bool
	gateway_class?: string @go(GatewayClass)
}
#KubernetesSecretsBackend: {
	backend?: "external-secrets" | "sealed-secrets" | "raw"
	store?:   string
	prefix?:  string
}
#KubernetesImagesDefaults: {
	pull_policy?: ("IfNotPresent" | "Always" | "Never") @go(PullPolicy)
	pull_secrets?: [...string] @go(PullSecrets)
}
#KubernetesPodDefaults: {
	priority_class?: string @go(PriorityClass)
	// Raw Kubernetes Toleration objects (Go []map[string]any) — genuine
	// passthrough, so each element stays OPEN.
	tolerations?: [...{...}]
	node_selector?: {[string]: string} @go(NodeSelector)
}
#KubernetesObservability: {
	service_monitor?:          bool   @go(ServiceMonitor)
	service_monitor_interval?: string @go(ServiceMonitorInterval)
}
#KubernetesResourceDefaults: {
	labels?: [string]:      string
	annotations?: [string]: string
}
