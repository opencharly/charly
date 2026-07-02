// Egress schemas for the Kubernetes manifests charly GENERATES (the Kustomize
// base/ + overlays/ tree). These validate charly's OWN typed-Go output, so they
// check STRUCTURE — the real egress failure mode (a missing/empty apiVersion,
// kind, or metadata.name; a malformed Kustomization) — rather than deep per-field
// k8s API types. Deep field validation is an INGRESS concern (user-authored
// manifests); egress of machine-generated manifests needs the envelope. Both are
// open beyond the envelope (`...`) since k8s specs vary too widely to close.
// Package-less → these join sharedCueSchema and resolve via egressDef's fallback.

// #K8sObject — the envelope every generated manifest (workload / service / pvc /
// ingress) must satisfy.
#K8sObject: {
	apiVersion: string & !=""
	kind:       string & !=""
	metadata: {
		name: string & !=""
		...
	}
	...
}

// #Kustomization — the base + overlay kustomization.yaml charly emits.
#Kustomization: {
	apiVersion: =~"^kustomize\\.config\\.k8s\\.io/"
	kind:       "Kustomization"
	resources: [...string]
	...
}
