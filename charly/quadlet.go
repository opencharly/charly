// quadlet.go — the RESIDUAL host-side quadlet surface after P11. The pod
// config-WRITE MECHANISM (GenerateQuadlet + its [Unit]/[Container]/[Service]/
// [Install] emitters + the QuadletConfig/CollectedSecret/ResolvedBindMount/
// ResolvedSidecar resolved-runtime types + the pure size/port/tunnel helpers, and
// the cloudflare tunnel-unit emitter) relocated to sdk/deploykit
// (deploykit_pod_aliases.go re-points the package-main call sites). The on-disk
// quadlet/systemd path + filename helpers (quadletDir/quadletFilename*/serviceName*/
// quadletExists*) MOVED to sdk/deploykit too (K4 lane B — shared with
// candy/plugin-deploy-pod's pod_lifecycle_resolve.go quadlet-mode move); see
// deploykit_pod_aliases.go's aliases.
package main
