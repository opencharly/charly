package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// BoxCmd groups build-mode commands that operate on charly.yml (or, in the
// case of BoxPullCmd, resolve registry/tag via charly.yml and then fetch the
// image into local storage so deploy-mode commands can read its OCI labels).
type BoxCmd struct {
	Build    BuildCmd      `cmd:"" help:"Build container boxes"`
	Generate GenerateCmd   `cmd:"" help:"Write .build/ (Containerfiles) for the named boxes (default: all enabled)"`
	Inspect  InspectCmd    `cmd:"" help:"Print resolved config for a box (JSON)"`
	List     ListCmd       `cmd:"" help:"List components from charly.yml"`
	Merge    MergeCmd      `cmd:"" help:"Merge small layers in a built container image"`
	New      NewCmd        `cmd:"" help:"Scaffold new components"`
	Pull     BoxPullCmd    `cmd:"" help:"Pull an image from its registry into local storage"`
	Pkg      BoxPkgCmd     `cmd:"" help:"Build standalone native package artifacts (.pkg.tar.zst/.rpm/.deb) for a candy's localpkg sources into dist/"`
	Validate ValidateCmd   `cmd:"" help:"Check charly.yml + candies, exit 0 or 1"`
	Labels   BoxLabelsCmd  `cmd:"" help:"Print a built image's OCI labels (the ai.opencharly.* capability contract; --format <key> for one value, --all for every label)"`
	Feature  BoxFeatureCmd `cmd:"" help:"Run a box's baked plan steps as acceptance tests against a disposable container (Agent Driven Evaluation, build scope)"`

	// Authoring verbs — added so the MCP tool surface (auto-reflected from
	// Kong) can author a project from scratch over RPC.
	Set       BoxSetCmd       `cmd:"" help:"Set a value in charly.yml by dot-path (e.g. box.foo.base fedora)"`
	AddCandy  BoxAddCandyCmd  `cmd:"" name:"add-candy" help:"Append a candy to a box's candy: list (idempotent)"`
	RmCandy   BoxRmCandyCmd   `cmd:"" name:"rm-candy" help:"Remove a candy from a box's candy: list"`
	Fetch     BoxFetchCmd     `cmd:"" help:"Pre-prime the remote-repo cache (default: opencharly/charly)"`
	Refresh   BoxRefreshCmd   `cmd:"" help:"Force re-clone of a remote project repo"`
	Write     BoxWriteCmd     `cmd:"" help:"Write file contents under the project root (escape hatch for free-form files)"`
	Cat       BoxCatCmd       `cmd:"" help:"Print file contents from under the project root"`
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
