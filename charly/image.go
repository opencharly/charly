package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
)

// BoxCmd groups build-mode commands that operate on charly.yml (or, in the
// case of BoxPullCmd, resolve registry/tag via charly.yml and then fetch the
// image into local storage so deploy-mode commands can read its OCI labels).
//
// `charly box` is a SHARED command group: the RETAINED verbs below are the core
// grammar spine (build → plugin-build; merge/labels/feature/reconcile). The
// generate/validate/new/pkg/inspect/list verbs are contributed as NESTED command
// providers by the COMPILED-IN candy/plugin-box, and the authoring verbs
// (set/add-candy/rm-candy/fetch/refresh/write/cat) by the COMPILED-IN
// candy/plugin-authoring (P14b) — each a command:<word> with
// CommandParent()=="box", attached into the embedded kong.Plugins below. This
// mirrors how a compiled-in command holder embeds kong.Plugins for its nested
// external subcommands.
type BoxCmd struct {
	// Plugins carries the nested command providers whose CommandParent()=="box"
	// (candy/plugin-box's generate/validate/new/pkg/inspect/list +
	// candy/plugin-authoring's set/add-candy/rm-candy/fetch/refresh/write/cat).
	// main() sets this to collectExternalCommandPlugins()'s nestedByParent["box"]
	// before kong.Parse.
	kong.Plugins

	Build     BuildCmd        `cmd:"" help:"Build container boxes"`
	Merge     MergeCmd        `cmd:"" help:"Merge small layers in a built container image"`
	Pull      BoxPullCmd      `cmd:"" help:"Pull an image from its registry into local storage"`
	Labels    BoxLabelsCmd    `cmd:"" help:"Print a built image's OCI labels (the ai.opencharly.* capability contract; --format <key> for one value, --all for every label)"`
	Feature   BoxFeatureCmd   `cmd:"" help:"Run a box's baked plan steps as acceptance tests against a disposable container (Agent Driven Evaluation, build scope)"`
	Reconcile BoxReconcileCmd `cmd:"" help:"Align cross-repo @github candy pins to the newest version (clears resolver newest-wins warnings)"`
}

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
		if !looksLikeFullRef(c.Box) && !IsRemoteImageRef(StripURLScheme(c.Box)) {
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

// looksLikeFullRef returns true if the image ref contains a registry segment
// (a "/" before any ":") — e.g. "ghcr.io/org/name:tag" — so it can be pulled
// without charly.yml resolution.
func looksLikeFullRef(ref string) bool {
	if strings.HasPrefix(ref, "@") {
		return false
	}
	slash := strings.Index(ref, "/")
	if slash < 0 {
		return false
	}
	colon := strings.Index(ref, ":")
	return colon < 0 || slash < colon
}

// FormatCLIError wraps top-level Kong errors with a friendly recommendation
// when the underlying cause is a missing local image (ErrImageNotLocal).
// Called from main() just before FatalIfErrorf so the exit path still passes
// through Kong's standard error rendering.
func FormatCLIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrImageNotLocal) {
		// ExtractMetadata wraps as "image not found in local storage: <ref>";
		// pull out the ref so we can render the recommendation.
		msg := err.Error()
		ref := strings.TrimPrefix(msg, ErrImageNotLocal.Error()+": ")
		return fmt.Errorf("image %q is not available locally.\nRun 'charly box pull %s' to fetch it first", ref, ref)
	}
	return err
}
