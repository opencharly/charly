package deploypod

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/spec"
)

// image_ensure.go — the pod-config-ensure-image leg, relocated from
// charly/host_build_pod_config_seams.go's hostBuildPodConfigEnsureImage/ensureImagePresent
// (K-wave W3a B6, the A2-proven peer-dispatch pattern: candy/plugin-preempt already reaches a
// COMPILED-IN provider word via a plain resolve+invoke; here the plugin reaches build:ensure the
// SAME way core's own dispatchBuildEnsure does, just via the plugin-to-plugin
// Executor.InvokeProvider leg instead of core's private providerRegistry). container.LocalImageExists
// / container.TransferImage are ALREADY portable (spec/container, used directly by candy/plugin-build
// per that package's own doc comment) — no seam needed for those two tiers either. The
// "pod-config-ensure-image" HostBuild seam + its core handler are DELETED; dispatchBuildEnsure
// itself STAYS core (it has other core-internal callers: plugin_executor_reverse.go,
// host_build_check_run.go — this leg's death removes ONE of dispatchBuildEnsure's several
// callers, not the function).
//
// Byte-for-byte parity with the deleted host body: run engine is hardcoded "podman" (the pod
// substrate always runs on podman, matching the deleted seam's hardcoded podmanRT.RunEngine), the
// three-tier fallback order is unchanged, and a failure at every tier collapses to
// spec.ErrImageNotLocal wrapping imageRef (never surfacing the underlying build:ensure error
// detail — matches the deleted body's own behavior).
const podRunEngine = "podman"

// ensureImagePresent guarantees imageRef is available in podman's local store, driving the store
// itself (no host round-trip): (1) already-present short-circuit, (2) cross-engine transfer when
// buildEngine differs and already has the image, (3) build:ensure peer-dispatch (pulls from the
// registry, falling back to a local build for a project charly.yml entry).
func ensureImagePresent(ctx context.Context, ex *sdk.Executor, imageRef, buildEngine string) error {
	if container.LocalImageExists(podRunEngine, imageRef) {
		return nil
	}
	if buildEngine != "" && buildEngine != podRunEngine && container.LocalImageExists(buildEngine, imageRef) {
		return container.TransferImage(buildEngine, podRunEngine, imageRef)
	}
	if err := dispatchBuildEnsurePeer(ctx, ex, imageRef, buildEngine); err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s", spec.ErrImageNotLocal, imageRef)
}

// dispatchBuildEnsurePeer InvokeProviders the compiled-in build:ensure word — the SAME word
// core's own dispatchBuildEnsure (charly/dispatch_build_ensure.go, unchanged, still used by its
// other callers) reaches via the core-private providerRegistry; a plugin reaches it via the
// generic Executor.InvokeProvider peer-dispatch leg instead (class-agnostic — "build"/"ensure" is
// treated identically to any other (class, word) pair).
func dispatchBuildEnsurePeer(ctx context.Context, ex *sdk.Executor, imageRef, buildEngine string) error {
	params, err := json.Marshal(spec.BuildEnsureRequest{
		Image:       imageRef,
		BuildEngine: buildEngine,
		RunEngine:   podRunEngine,
	})
	if err != nil {
		return err
	}
	out, err := ex.InvokeProvider(ctx, "build", "ensure", sdk.OpBuild, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	var reply spec.BuildEnsureReply
	if len(out) > 0 {
		if uerr := json.Unmarshal(out, &reply); uerr != nil {
			return uerr
		}
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}
