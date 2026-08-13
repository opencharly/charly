// schema/helm.cue — the SELF-CONTAINED CUE defs for the `step:helm-release` install
// step and the `verb:helm` check verb (the helm words, Factory Unit 1). Ships over
// Describe (schema_cue) + drives the generated Go params; references NO base def so
// it compiles standalone (BuildCapabilities compiles it alone, failing loudly if
// broken) AND splices onto the base (base ++ plugin is a def-name collision check,
// not a base-reference resolver).
//
// The step is a plugin-contributed `external:helm-release` install-step KIND (F3):
// its OPAQUE Payload is this #HelmReleaseStep, carried through the InstallPlan IR and
// validated against this def at authoring. The verb is the declarative release-status
// assertion (the `verb:kube` analog): its method + method-exclusive modifiers ride the
// desugared plugin input, validated against #HelmInput.

// #HelmReleaseStep is the `step:helm-release` install step's plugin_input: the
// helm upgrade --install invocation the step performs IN-VENUE (against the venue's
// kubeconfig — /etc/rancher/k3s/k3s.yaml on a k3s guest). Field set confirmed by the
// Unit 1 schema spike.
#HelmReleaseStep: {
	// repo — the chart repository URL (helm repo add + --repo). Optional: a chart may
	// come from a pre-added repo or an OCI reference.
	repo?: string
	// chart — the chart name (required). May be a bare name (resolved via repo) or a
	// full reference.
	chart!: string
	// version — the chart version to pin (helm upgrade --install --version).
	version?: string
	// release — the release name (required).
	release!: string
	// namespace — the target namespace (helm --namespace; created if absent).
	namespace?: string
	// values — inline values (helm --set key=value per leaf, or a values map).
	values?: {[string]: _}
	// values_files — values YAML files (helm -f), venue-relative paths.
	values_files?: [...string]
	// wait — wait for the release to become ready (helm --wait).
	wait?: bool
	// timeout — the wait timeout (helm --timeout, e.g. "5m").
	timeout?: string
}

// #HelmInput is the `verb:helm` check verb's plugin_input: the method name plus its
// method-exclusive modifiers. The verb's PRIMARY input field is `method`, so
// `helm: release-exists` desugars to {method: "release-exists"}.
#HelmInput: {
	// method — the helm assertion method.
	method: ("release-exists" | "status" | "revision" | "values-hash")
	// release — the release name to assert on (required).
	release!: string
	// namespace — the release's namespace (default "default").
	namespace?: string
	// status — method:status — the expected release status (default "deployed").
	status?: string
	// revision — method:revision — the minimum release revision to assert (≥).
	revision?: int
	// values_hash — method:values-hash — the expected SHA-256 of the release's
	// rendered values (helm get values -o json | sha256sum).
	values_hash?: string
}
