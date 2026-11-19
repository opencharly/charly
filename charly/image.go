package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/opencharly/spec/spec"
)

// BoxCmd groups build-mode commands that operate on charly.yml.
//
// `charly box` is a SHARED command group with NO retained core verb: every `charly box <word>`
// subcommand is contributed as a NESTED command provider (CommandParent()=="box") by a COMPILED-IN
// plugin — candy/plugin-box's generate/validate/new/pull/build/inspect/list/labels/merge/
// reconcile/feature, and candy/plugin-authoring's set/add-candy/rm-candy/fetch/refresh/write/cat
// (P14b) — attached into the embedded kong.Plugins below. `box feature` (build-scope Agent Driven
// Evaluation) was the LAST retained core verb; it moved to candy/plugin-box's command:feature (which
// bridges to the plugin-check engine over InvokeProvider) in cone-C #31, once the CommandParent-aware
// registry key (#44) let it coexist with candy/plugin-feature's top-level command:feature. So BoxCmd
// is now PURELY the plugin-attachment holder — the core box grammar knows zero box verbs.
type BoxCmd struct {
	// Plugins carries the nested command providers whose CommandParent()=="box"
	// (candy/plugin-box's generate/validate/new/pull/build/inspect/list/labels/merge/reconcile/feature
	// + candy/plugin-authoring's set/add-candy/rm-candy/fetch/refresh/write/cat).
	// main() sets this to collectExternalCommandPlugins()'s nestedByParent["box"]
	// before kong.Parse.
	kong.Plugins
}

// BoxCmd holds no verb of its own: every `charly box <word>` verb is a command:<word> plugin
// (candy/plugin-box), and main.go knows zero box verbs, so the struct is purely the
// plugin-attachment holder. candy/plugin-box serves all of them — generate, validate, new, pull,
// build, inspect, list, labels, merge, reconcile and feature. dispatchPull runs the ensure-image
// work itself via InvokeProvider(build:ensure); dispatchBuild runs the build body itself
// (NormalizeBoxArgs → remote-ref pivot via buildkit.DetectRemoteBuildRef +
// HostBuild("remote-image-resolve") → build-activity flock → InvokeProvider(build:box) →
// retention prune); and `charly box feature run` is command:feature, bridging to the plugin-check
// engine over InvokeProvider. reconcile carries no core-only coupling
// (candy/plugin-box/reconcile.go). See sdk/deploykit/write_labels.go +
// candy/plugin-box/{merge_cmd,reconcile,box}.go's dispatchPull/dispatchBuild. The OCI-label
// surface lives in sdk/deploykit + spec/container; see /charly-internals:capabilities.
//
// What stays core is the genuine floor a sdk-only candy cannot run: the git clone/cache
// (EnsureRepoDownloaded → clone/cache, K1), which the candy reaches over the thin
// HostBuild("remote-image-resolve") seam (host_build_remote_image_resolve.go — the box-RESOLVE
// runs plugin-side, over that seam); and the build-engine RESOLVE legs
// (host_build_buildengine.go). `charly vm build`'s PREP+RESOLVE runs plugin-side in
// candy/plugin-vm's resolveVmBuild (vm_build_resolve.go), reaching the builder-image auto-build
// via InvokeProvider(build, box) rather than a core reentry. The ensure-image ORCHESTRATION is
// candy/plugin-build's build:ensure word, dispatched via dispatchBuildEnsure
// (dispatch_build_ensure.go) — a thin CLI-independent host helper, outside this command-dispersal
// accounting.

// FormatCLIError wraps top-level Kong errors with a friendly recommendation
// when the underlying cause is a missing local image (spec.ErrImageNotLocal).
// Called from main() just before FatalIfErrorf so the exit path still passes
// through Kong's standard error rendering.
func FormatCLIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, spec.ErrImageNotLocal) {
		// ExtractMetadata (or any other wrapper — a compiled-in command plugin's generic
		// dispatchInProcCommand "command %q: %w" wrap included, K3 reentry-class dissolution)
		// renders as "...image not found in local storage: <ref>"; find the marker WHEREVER it
		// lands in the message (not just as a whole-message prefix — that broke the moment a
		// command-dispatch wrap started prefixing it) and pull out the ref from after it.
		marker := spec.ErrImageNotLocal.Error() + ": "
		msg := err.Error()
		if idx := strings.LastIndex(msg, marker); idx >= 0 {
			ref := msg[idx+len(marker):]
			return fmt.Errorf("image %q is not available locally.\nRun 'charly box pull %s' to fetch it first", ref, ref)
		}
	}
	return err
}
