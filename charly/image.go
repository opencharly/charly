package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/opencharly/sdk/kit"
)

// BoxCmd groups build-mode commands that operate on charly.yml.
//
// `charly box` is a SHARED command group with NO retained core verb: every `charly box <word>`
// subcommand is contributed as a NESTED command provider (CommandParent()=="box") by a COMPILED-IN
// plugin — candy/plugin-box's generate/validate/new/pkg/pull/build/inspect/list/labels/merge/
// reconcile/feature, and candy/plugin-authoring's set/add-candy/rm-candy/fetch/refresh/write/cat
// (P14b) — attached into the embedded kong.Plugins below. `box feature` (build-scope Agent Driven
// Evaluation) was the LAST retained core verb; it moved to candy/plugin-box's command:feature (which
// bridges to the plugin-check engine over InvokeProvider) in cone-C #31, once the CommandParent-aware
// registry key (#44) let it coexist with candy/plugin-feature's top-level command:feature. So BoxCmd
// is now PURELY the plugin-attachment holder — the core box grammar knows zero box verbs.
type BoxCmd struct {
	// Plugins carries the nested command providers whose CommandParent()=="box"
	// (candy/plugin-box's generate/validate/new/pkg/pull/build/inspect/list/labels/merge/reconcile/feature
	// + candy/plugin-authoring's set/add-candy/rm-candy/fetch/refresh/write/cat).
	// main() sets this to collectExternalCommandPlugins()'s nestedByParent["box"]
	// before kong.Parse.
	kong.Plugins
}

// MIGRATION INVENTORY (north-star §4.4): the box command-dispersal is now COMPLETE for BoxCmd —
// every `charly box <word>` verb is a command:<word> plugin (candy/plugin-box), so BoxCmd holds no
// verb of its own (main.go knows zero box verbs; the struct is purely the plugin-attachment holder).
// Trace: generate/validate/new/pkg/pull/build/inspect/list/labels/merge/reconcile externalized
// across K3/P14/Cutover-B (labels fully in K3, merge at P14, reconcile at Cutover B unit 3+4 [no
// core-only coupling — candy/plugin-box/reconcile.go]; pull FULLY externalized at K3 #39 —
// candy/plugin-box's dispatchPull runs the ensure-image work itself via InvokeProvider(build:ensure);
// BoxPullCmd + the hidden __box-pull reentry are DELETED; build FULLY externalized at P8b —
// candy/plugin-box's dispatchBuild runs the former BuildCmd.Run body ITSELF (NormalizeBoxArgs →
// remote-ref pivot via buildkit.DetectRemoteBuildRef + HostBuild("remote-image-resolve") →
// build-activity flock → InvokeProvider(build:box) → retention prune), so BuildCmd + the hidden
// __box-build reentry are DELETED, matching the pull move); and feature at cone-C #31 — `charly box
// feature run` is now candy/plugin-box's command:feature (bridging to the plugin-check engine over
// InvokeProvider), the former in-core BoxFeatureCmd/BoxFeatureRunCmd + hostFeatureBox DELETED.
// pkg_cmd.go documents its own UNTIL-K1 note. See charly/labels.go +
// candy/plugin-box/{merge_cmd,reconcile,box}.go's dispatchPull/dispatchBuild.
//
// The host-coupled remainder that DID stay core is the genuine floor a sdk-only candy cannot run:
// remote_image.go's ResolveRemoteImage (EnsureRepoDownloaded → clone/cache, K1), reached by the
// candy over the thin HostBuild("remote-image-resolve") seam (host_build_remote_image_resolve.go); the
// build-engine RESOLVE legs (host_build_buildengine.go); and the bootstrap-builder pre-pass
// (ensureBuilderImageBuilt → dispatchBoxBuild, now in host_build_vm_build.go, whose one caller is the
// kind:vm bootstrap path). The former core ensure-image helper (core-min wave 3, build-engine cluster
// relocation) is DELETED — its ensure-image ORCHESTRATION moved to candy/plugin-build's build:ensure
// word, dispatched via dispatchBuildEnsure (dispatch_build_ensure.go), a thin CLI-independent host
// helper — not part of this command-dispersal accounting at all.

// FormatCLIError wraps top-level Kong errors with a friendly recommendation
// when the underlying cause is a missing local image (kit.ErrImageNotLocal).
// Called from main() just before FatalIfErrorf so the exit path still passes
// through Kong's standard error rendering.
func FormatCLIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, kit.ErrImageNotLocal) {
		// ExtractMetadata (or any other wrapper — a compiled-in command plugin's generic
		// dispatchInProcCommand "command %q: %w" wrap included, K3 reentry-class dissolution)
		// renders as "...image not found in local storage: <ref>"; find the marker WHEREVER it
		// lands in the message (not just as a whole-message prefix — that broke the moment a
		// command-dispatch wrap started prefixing it) and pull out the ref from after it.
		marker := kit.ErrImageNotLocal.Error() + ": "
		msg := err.Error()
		if idx := strings.LastIndex(msg, marker); idx >= 0 {
			ref := msg[idx+len(marker):]
			return fmt.Errorf("image %q is not available locally.\nRun 'charly box pull %s' to fetch it first", ref, ref)
		}
	}
	return err
}
