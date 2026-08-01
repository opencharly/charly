package deploypod

import (
	"context"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// resolve_ref.go — the deploy-key→(box-name, image-ref) resolution the pod config/start paths do,
// resolved PLUGIN-SIDE (the deploy-config-resolve reverse-leg SEAM COLLAPSE, #55 Cone A Unit 2).
// The former host seam and its three host-side resolvers (charly/deploy.go, charly/host_build_pod_config_seams.go)
// are DELETED — see the CHANGELOG for the retired names. This file self-resolves using only what a
// plugin can reach placement-invariantly:
//   - the per-host deploy overlay via the EXISTING "pod-config-load-deploy" seam (loadDeploy → dc),
//   - the PROJECT fallback via the shared loaderkit.LoadUnifiedViaExecutor helper + the
//     "deploy-plugins-connect" seam's project dir (the SAME plugin-side loader path
//     candy/plugin-bundle's resolveTreeViaLoader uses), and
//   - kit.ResolveShellImageRef / kit.LocalImageExists (pure).
// Byte-for-byte the former host resolvers' two-tier semantics: per-host Image (DeployKey then bare
// key) → project Bundle[box].Image → the key itself; and the add_candy-overlay resolved_image
// preference over the base-name resolution (DeployKey then bare key), gated on the overlay image
// actually existing locally.

const deployPluginsConnectKind = "deploy-plugins-connect"

// resolveDeployRefLocal is the plugin-side replacement for the former "pod-config-resolve-ref" host
// seam. explicitRef (set only by `charly bundle from-box`) short-circuits both outputs, exactly as
// the former seam did.
func resolveDeployRefLocal(ctx context.Context, ex *sdk.Executor, box, instance, tag, explicitRef string) (deployBoxName, imageRef string, err error) {
	if explicitRef != "" {
		return explicitRef, explicitRef, nil
	}
	dc, err := loadDeploy(ctx, ex, "pod resolve deploy ref")
	if err != nil {
		return "", "", err
	}
	deployBoxName = resolveDeployBoxNameLocal(ctx, ex, dc, box, instance)
	if ov := resolvedOverlayImage(dc, box, instance); ov != "" && kit.LocalImageExists("podman", ov) {
		return deployBoxName, ov, nil
	}
	return deployBoxName, kit.ResolveShellImageRef("", deployBoxName, tag), nil
}

// resolveDeployBoxNameLocal mirrors the former host box-name resolver: the deploy entry's declared
// box (per-host then project), falling back to the key itself (the key==image convention).
func resolveDeployBoxNameLocal(ctx context.Context, ex *sdk.Executor, dc *deploykit.BundleConfig, box, instance string) string {
	if img := deployKeyToBoxLocal(ctx, ex, dc, box, instance); img != "" {
		return img
	}
	return box
}

// deployKeyToBoxLocal mirrors the former host deploy-key→box loader-read: user (per-host) overlay
// wins over the project config; each is tried DeployKey-then-bare-key.
func deployKeyToBoxLocal(ctx context.Context, ex *sdk.Executor, dc *deploykit.BundleConfig, box, instance string) string {
	if dc != nil {
		if e, ok := dc.Lookup(box, instance); ok && e.Image != "" {
			return e.Image
		}
		if e, ok := dc.LookupKey(box); ok && e.Image != "" {
			return e.Image
		}
	}
	// Project-level fallback — the loader-over-executor path (parity with the former host resolver's
	// LoadUnified → ProjectBundleConfig). Degrades to "" (→ the key) on any connect/load failure,
	// matching the former resolver's no-entry → "" outcome.
	var pre spec.DeployPluginsConnectReply
	if err := hostBuild(ctx, ex, deployPluginsConnectKind, spec.DeployPluginsConnectRequest{Path: box}, &pre); err != nil || pre.Dir == "" {
		return ""
	}
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, pre.Dir)
	if err != nil || !ok || uf == nil {
		return ""
	}
	pc := deploykit.ProjectBundleConfig(uf)
	if pc == nil {
		return ""
	}
	if e, ok := pc.Bundle[box]; ok && e.Image != "" {
		return e.Image
	}
	return ""
}

// resolvedOverlayImage mirrors the former host resolved_image lookup: the concrete add_candy-overlay
// image ref persisted per-host (BundleNode.ResolvedImage), DeployKey-then-bare-key.
func resolvedOverlayImage(dc *deploykit.BundleConfig, box, instance string) string {
	if dc == nil {
		return ""
	}
	if e, ok := dc.Lookup(box, instance); ok && e.ResolvedImage != "" {
		return e.ResolvedImage
	}
	if e, ok := dc.LookupKey(box); ok && e.ResolvedImage != "" {
		return e.ResolvedImage
	}
	return ""
}
