package check

// live_image.go — the ONE answer to "which image is this LIVE deployment running?".
//
// A `charly check live` gather evaluates a RUNNING deployment, so its candy/box acceptance plan
// comes from the `ai.opencharly.description` label baked into the image that container is actually
// running — read off the container itself, never re-derived.
//
// The three live gathers (live_gather.go's pod arm, members.go's `on:` driver var-resolver,
// feature_run_gather.go's deploy-scope ADE arm) previously answered it with a BUILD-time
// re-resolution instead: deploy-key → box name → `ResolveShellImageRef(registry, name, "")` with an
// EMPTY tag, which falls through to a short-name search of local container storage
// (spec/container/local_image_coneb.go's ResolveLocalImageRef). That search ranks candidates by the
// content-stable label CalVer (`ai.opencharly.version` — identical across every build of one candy
// version) and then by the tag CalVer, which `ExtractCalVerTag` reports as EMPTY for a bed tag
// (`check-<bed>-<YYYY.DDD.HHMM>` is not three numeric dot-parts). With both keys tied for every
// candidate the sort falls to its lexicographic-ascending last resort, so the OLDEST local build of
// the box won and supplied the plan. RCA'd live 2026-08-05 on the check-docs bed: `check live` read
// `docs-site-app:check-docs-2026.216.1150` (22h old) while the container ran
// `:check-docs-2026.217.0938`, so a step asserting a literal the branch had just changed failed
// against the PREVIOUS build's baked expectation — structurally, no check literal could ever pass
// its own R10 bed.
//
// The generic fix is to ask the container. container.ContainerImageRef is THE single
// container→image-ref inspector (spec/container/image_inspect.go) and the sibling live path
// resolve_endpoint.go:118 already answers this same question with it; this file makes the plan
// gathers use it too (R3: one path, one answer).
//
// containerImageRef is a package-level var for the same reason container.InspectLabels is: it lets
// a test exercise the resolution without a live container store. Override THIS var, never a
// re-export.

import (
	"fmt"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/spec"
)

// containerImageRef reads the image ref a running container was created from. Package-level var
// for testability; defaults to the canonical spec/container inspector.
var containerImageRef = container.ContainerImageRef

// liveDeployMetadata returns the BoxMetadata baked into the image containerName is RUNNING — the
// sole image-identity source for every live-mode gather. A container that cannot be inspected is a
// hard error: a live check evaluates a running deployment, so an absent container means the check
// cannot be answered, never that some other local image may stand in for it. A running container
// whose image carries no `ai.opencharly.*` labels (a plain registry base) yields (nil, nil) —
// ExtractMetadata's own not-an-opencharly-image signal, which each caller handles.
func liveDeployMetadata(engine, containerName string) (*spec.BoxMetadata, error) {
	ref, err := containerImageRef(engine, containerName)
	if err != nil {
		return nil, fmt.Errorf("check live: reading the image container %q is running: %w", containerName, err)
	}
	if strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("check live: container %q reports no image", containerName)
	}
	return deploykit.ExtractMetadata(engine, ref)
}
