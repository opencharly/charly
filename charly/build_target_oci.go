package main

// build_target_oci.go — residual helper for the build/emit pipeline. The pod-overlay
// Containerfile WALKER (deploykit.OCITarget) MOVED to sdk/deploykit/oci_target.go (P11c — the overlay-
// BUILD dissolution): the kind-blind walker is a shared render M-mechanism importable by BOTH
// candy/plugin-build (box) and candy/plugin-deploy-pod (overlay), and the per-step DISPATCH
// stays core (charly/oci_step_emit.go's ociEmitStep — the kind-blind host-side M-mechanism the
// candy reaches over HostBuild("step-emit","oci-emit-step")). The host "overlay" host-builder
// (charly/build_overlay.go) is the prep+resolve+envelope M-seam; the candy renders the overlay
// Containerfile in its own code. This file keeps only the FormatDef cache-mount bridge the
// deploy host helpers still consume — it carries no target struct + no render logic after the
// P11c relocation.

// formatDefCacheMountDefs returns the cache mounts as the type
// RenderTemplate's InstallContext expects. FormatDef.CacheMount is the
// source of truth; this is a no-op bridge.
func formatDefCacheMountDefs(f *FormatDef) []CacheMountDef {
	if f == nil {
		return nil
	}
	return f.CacheMount
}
