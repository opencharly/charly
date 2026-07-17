package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/opencharly/sdk/kit"
)

// BoxCmd groups build-mode commands that operate on charly.yml (or, in the
// case of BoxPullCmd, resolve registry/tag via charly.yml and then fetch the
// image into local storage so deploy-mode commands can read its OCI labels).
//
// `charly box` is a SHARED command group: the RETAINED verbs below are the core
// grammar spine (build → plugin-build; merge/feature/reconcile). The
// generate/validate/new/pkg/inspect/list/labels verbs are contributed as NESTED command
// providers by the COMPILED-IN candy/plugin-box, and the authoring verbs
// (set/add-candy/rm-candy/fetch/refresh/write/cat) by the COMPILED-IN
// candy/plugin-authoring (P14b) — each a command:<word> with
// CommandParent()=="box", attached into the embedded kong.Plugins below. This
// mirrors how a compiled-in command holder embeds kong.Plugins for its nested
// external subcommands.
type BoxCmd struct {
	// Plugins carries the nested command providers whose CommandParent()=="box"
	// (candy/plugin-box's generate/validate/new/pkg/inspect/list/labels +
	// candy/plugin-authoring's set/add-candy/rm-candy/fetch/refresh/write/cat).
	// main() sets this to collectExternalCommandPlugins()'s nestedByParent["box"]
	// before kong.Parse.
	kong.Plugins

	Build     BuildCmd        `cmd:"" help:"Build container boxes"`
	Merge     MergeCmd        `cmd:"" help:"Merge small layers in a built container image"`
	Pull      BoxPullCmd      `cmd:"" help:"Pull an image from its registry into local storage"`
	Feature   BoxFeatureCmd   `cmd:"" help:"Run a box's baked plan steps as acceptance tests against a disposable container (Agent Driven Evaluation, build scope)"`
	Reconcile BoxReconcileCmd `cmd:"" help:"Align cross-repo @github candy pins to the newest version (clears resolver newest-wins warnings)"`
}

// MIGRATION INVENTORY (north-star §4.4): the RETAINED verbs above (build/merge/pull/feature/
// reconcile) are UNTIL-K5 (command-dispersal — every CLI verb becomes a command plugin; main.go
// knows zero verbs). Each moves to its own command:<word> plugin as its build/deploy-cone engine
// externalizes (mirroring generate/validate/new/pkg/inspect/list/labels above, P14-rest trace,
// 2026-07 — labels externalized fully in K3, no host reentry left; see charly/labels.go): merge.go
// and pkg_cmd.go already document their own UNTIL-K5/K1 notes; build/pull/feature/reconcile are
// the remaining residue in this struct.

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
		if !kit.LooksLikeFullRef(c.Box) && !IsRemoteImageRef(StripURLScheme(c.Box)) {
			if cfg == nil {
				return fmt.Errorf("short name %q with --tag requires a project directory with charly.yml", c.Box)
			}
			resolved, err := cfg.ResolveBox(c.Box, c.Tag, dir, ResolveOpts{})
			if err != nil {
				return err
			}
			ref := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
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
