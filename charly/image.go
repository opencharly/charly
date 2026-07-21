package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
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
// host reentry left for any of the three; pull externalized at FINAL/K5 unit 6a M4c, build at M4d
// [the CLI-only mirror of pull's move: BoxPullCmd/BuildCmd's OWN grammar/dispatch are now the
// compiled-in candy/plugin-box `pull`/`build` words; both Run bodies are UNCHANGED and stay behind
// the hidden `__box-pull`/`__box-build` reentries over HostBuild("cli")]; see charly/labels.go +
// candy/plugin-box/merge_cmd.go + candy/plugin-box/reconcile.go + candy/plugin-box/box.go's
// dispatchPull/dispatchBuild): pkg_cmd.go already documents its own UNTIL-K1 note; feature is the
// remaining residue in this struct.
//
// ensure_image.go + remote_image.go + BuildCmd.Run()'s own internals (bootstrap-builder execution,
// remote-ref resolve/download/scan, retention pruning) are NOT CLI-dispersal residue — the M4d
// scoping trace (FINAL/K5 unit 6a) re-classified them from a K5-dispersal IOU to the K1/K3-ENGINE
// family (loader/build-engine cone, moves with those waves, never a CLI-verb tail-end guess): both
// EnsureImagePresent and RemoteImageContext.BuildImage construct + call BuildCmd.Run() DIRECTLY at
// the Go level, never through Kong/CLI, so the command-dispersal move above does not touch them.

// BoxPullCmd fetches an image from its registry into the local container
// engine so deploy-mode commands can read its OCI labels. Accepts three
// input forms:
//
//   - short name (e.g. "jupyter")           — resolves registry + tag via
//     charly.yml (requires a project directory)
//   - fully-qualified ref ("ghcr.io/...:v") — pulled as-is
//   - remote ref ("@github.com/org/repo/box[:version]") — downloads the
//     repo and pulls the registry ref from its charly.yml
type BoxPullCmd struct {
	Box      string `arg:"" help:"Box name (short, resolved via charly.yml), fully-qualified ref, or @github.com/org/repo/box[:version]"`
	Tag      string `long:"tag" help:"Image CalVer tag when resolving a short name (empty = resolve from charly.yml metadata or error with explicit guidance)"`
	Platform string `long:"platform" help:"Target platform (default: host)"`
}

func (c *BoxPullCmd) Run() error {
	// `charly box pull` is the operator-facing alias for the canonical
	// EnsureImagePresent path: pull from registry, fall back to a
	// local build when the identifier maps to a project charly.yml
	// entry. Same contract as BuilderRun, the check preflight, and
	// EnsureImage in transfer.go (R3, no per-command divergence).
	dir, _ := os.Getwd()
	cfg, _ := LoadConfig(dir)
	if c.Tag != "" {
		// Tag override: only meaningful for short-name input. Resolve
		// the canonical short-name ref FIRST so the build-fallback
		// path picks up the requested tag.
		if !kit.LooksLikeFullRef(c.Box) && !spec.IsRemoteImageRef(kit.StripURLScheme(c.Box)) {
			if cfg == nil {
				return fmt.Errorf("short name %q with --tag requires a project directory with charly.yml", c.Box)
			}
			resolved, err := cfg.ResolveBox(c.Box, c.Tag, dir, ResolveOpts{})
			if err != nil {
				return err
			}
			ref := kit.ResolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
			return EnsureImagePresent(context.Background(), ref, cfg, dir)
		}
	}
	return EnsureImagePresent(context.Background(), c.Box, cfg, dir)
}

// kit.LooksLikeFullRef (P12a: relocated to sdk/kit/local_image.go — it had 4
// core callers beyond this file, R3 single-source) returns true if the image
// ref contains a registry segment (a "/" before any ":") — e.g.
// "ghcr.io/org/name:tag" — so it can be pulled without charly.yml resolution.

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
