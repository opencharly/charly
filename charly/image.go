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
// BoxPullCmd + the hidden __box-pull reentry are DELETED; build at M4d — the compiled-in `build`
// word owns the grammar/dispatch, BuildCmd's Run body stays behind the hidden `__box-build` reentry
// over HostBuild("cli")); and feature at cone-C #31 — `charly box feature run` is now
// candy/plugin-box's command:feature (bridging to the plugin-check engine over InvokeProvider), the
// former in-core BoxFeatureCmd/BoxFeatureRunCmd + hostFeatureBox DELETED. pkg_cmd.go documents its
// own UNTIL-K1 note. See charly/labels.go + candy/plugin-box/{merge_cmd,reconcile,box}.go.
//
// remote_image.go + BuildCmd.Run()'s own internals (bootstrap-builder execution, remote-ref
// resolve/download/scan, retention pruning) are NOT CLI-dispersal residue — the M4d scoping trace
// (FINAL/K5 unit 6a) re-classified them from a K5-dispersal IOU to the K1/K3-ENGINE family
// (loader/build-engine cone, moves with those waves, never a CLI-verb tail-end guess):
// buildRemote (build.go) resolves the remote ref host-side (ResolveRemoteImage, K1) then runs the
// cached source through BuildCmd.Run() DIRECTLY at the Go level, never through Kong/CLI (the
// CLI-reentry `charly box build @ref` path — the build-DRIVE half moved to build:box in
// candy/plugin-build, K3 #39), so the command-dispersal move above does not touch it. The former core ensure-image helper
// (core-min wave 3, build-engine cluster relocation) is DELETED — its ensure-image ORCHESTRATION
// moved to candy/plugin-build's build:ensure word, dispatched via dispatchBuildEnsure
// (dispatch_build_ensure.go), which is itself a thin, CLI-independent host helper — not part of
// this command-dispersal accounting at all.

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
