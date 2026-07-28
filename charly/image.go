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
// `charly box` is a SHARED command group: the RETAINED verb below is the core
// grammar spine (feature). The
// generate/validate/new/pkg/pull/build/inspect/list/labels/merge/reconcile verbs are contributed
// as NESTED command providers by the COMPILED-IN candy/plugin-box, and the authoring verbs
// (set/add-candy/rm-candy/fetch/refresh/write/cat) by the COMPILED-IN
// candy/plugin-authoring (P14b) — each a command:<word> with
// CommandParent()=="box", attached into the embedded kong.Plugins below. This
// mirrors how a compiled-in command holder embeds kong.Plugins for its nested
// external subcommands.
type BoxCmd struct {
	// Plugins carries the nested command providers whose CommandParent()=="box"
	// (candy/plugin-box's generate/validate/new/pkg/pull/build/inspect/list/labels/merge/reconcile
	// + candy/plugin-authoring's set/add-candy/rm-candy/fetch/refresh/write/cat).
	// main() sets this to collectExternalCommandPlugins()'s nestedByParent["box"]
	// before kong.Parse.
	kong.Plugins

	Feature BoxFeatureCmd `cmd:"" help:"Run a box's baked plan steps as acceptance tests against a disposable container (Agent Driven Evaluation, build scope)"`
}

// MIGRATION INVENTORY (north-star §4.4): the RETAINED verb above (feature) is UNTIL-K5
// (command-dispersal — every CLI verb becomes a command plugin; main.go knows zero verbs). Each
// moves to its own command:<word> plugin as its build/deploy-cone engine externalizes (mirroring
// generate/validate/new/pkg/pull/build/inspect/list/labels/merge/reconcile above, P14-rest trace,
// 2026-07 — labels externalized fully in K3, merge externalized at P14, reconcile externalized at
// Cutover B unit 3+4 [it had no core-only coupling at all — see candy/plugin-box/reconcile.go], no
// host reentry left for any of the three; pull FULLY externalized (K3 #39 fold — candy/plugin-box's
// dispatchPull now runs the ensure-image work itself via InvokeProvider(build:ensure), reaching the
// registry ref off the resolved-project envelope; BoxPullCmd + the hidden __box-pull reentry are
// DELETED), build FULLY externalized at P8b [candy/plugin-box's dispatchBuild now runs the former
// BuildCmd.Run body ITSELF — NormalizeBoxArgs → remote-ref pivot (buildkit.DetectRemoteBuildRef +
// → build-activity flock → InvokeProvider(build:box) → retention prune — so BuildCmd + the hidden
// __box-build reentry are DELETED, matching the pull move]; see charly/labels.go +
// candy/plugin-box/merge_cmd.go + candy/plugin-box/reconcile.go + candy/plugin-box/box.go's
// dispatchPull/dispatchBuild): pkg_cmd.go already documents its own UNTIL-K1 note; feature is the
// remaining residue in this struct.
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
