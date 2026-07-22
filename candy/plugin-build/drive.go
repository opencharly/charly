package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sync/errgroup"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// jobsFallback is the outer image-level concurrency (images per DAG level) used when the
// host-resolved reply.Jobs is unset (< 1). Mirrors the former host-side buildImages fallback.
const jobsFallback = 4

// driveConfig is the candy-side drive model — the small set of resolved knobs the podman drive
// reads. Engine/EngineName/Platform/Cache/Jobs/PodmanJobs come from the host build-resolve reply
// (Platform + PodmanJobs already resolved host-side; Jobs/Cache are the config-resolved tunables);
// Push/NoCache are raw CLI flags the candy reads directly from the BuildRequest (the reply does
// NOT carry NoCache). Dir is the project/build-context dir the podman build runs in.
type driveConfig struct {
	Engine     string // build engine binary (e.g. "podman")
	EngineName string // runtime build-engine name ("podman"/"docker")
	Platform   string // target platform (host-resolved; empty on a push build)
	Cache      string // resolved --cache mode
	Push       bool   // --push
	NoCache    bool   // --no-cache
	Jobs       int    // outer concurrency (images per DAG level)
	PodmanJobs int    // inner concurrency (podman build --jobs; already resolved host-side)
	Dir        string // project dir the podman build runs in
}

// resolveRequest copies the host-constructed spec.BuildRequest 1:1 into the spec.BuildResolveRequest
// envelope the host build-resolve seam consumes, stamping GenerateOnly. Shared by the build and
// generate paths (R3): the ONLY difference between them is GenerateOnly.
func resolveRequest(req spec.BuildRequest, generateOnly bool) spec.BuildResolveRequest {
	return spec.BuildResolveRequest{
		Boxes:           req.Boxes,
		Tag:             req.Tag,
		Dir:             req.Dir,
		IncludeDisabled: req.IncludeDisabled,
		DevLocalPkg:     req.DevLocalPkg,
		Push:            req.Push,
		Platform:        req.Platform,
		Cache:           req.Cache,
		NoCache:         req.NoCache,
		Jobs:            int64(req.Jobs),
		PodmanJobs:      int64(req.PodmanJobs),
		GenerateOnly:    generateOnly,
	}
}

// resolveBuild runs the host loader/prep RESOLVE over the F10 HostBuild seam: it marshals the
// BuildResolveRequest, calls HostBuild("build-prep", …), and decodes the drive-model reply
// (envelope + order/levels/boxes/tunables, NO Containerfile content). A resolve FAILURE rides
// reply.Error (reply-error convention; the RPC itself succeeds) and is surfaced as a Go error.
// Shared by runBoxBuild + runBoxGenerate (R3). plugin-build renders Containerfiles itself via
// deploykit.Generator (#67 render-DRIVE move).
func resolveBuild(ctx context.Context, ex *sdk.Executor, req spec.BuildRequest, generateOnly bool) (spec.BuildResolveReply, error) {
	rrJSON, err := json.Marshal(resolveRequest(req, generateOnly))
	if err != nil {
		return spec.BuildResolveReply{}, err
	}
	replyJSON, err := ex.HostBuild(ctx, "build-prep", rrJSON)
	if err != nil {
		return spec.BuildResolveReply{}, err
	}
	var reply spec.BuildResolveReply
	if err := json.Unmarshal(replyJSON, &reply); err != nil {
		return spec.BuildResolveReply{}, fmt.Errorf("decode build-prep reply: %w", err)
	}
	if reply.Error != "" {
		return spec.BuildResolveReply{}, fmt.Errorf("%s", reply.Error)
	}
	return reply, nil
}

// runBoxBuild is the candy-side image-build DRIVE behind the build:box word: it resolves the
// drive-model host-side (build-resolve), builds every selected image with the configured engine
// (per-image lock + inline merge), pushes (podman --push after merge), and returns the built image
// refs (the BuildReply.Written provenance). The heavy engine RESOLVE + the layer MERGE stay
// host-side over HostBuild; the podman exec + the build-order orchestration live HERE.
func runBoxBuild(ctx context.Context, ex *sdk.Executor, req spec.BuildRequest) ([]string, error) {
	reply, err := resolveBuild(ctx, ex, req, false)
	if err != nil {
		return nil, err
	}
	cfg := driveConfig{
		Engine:     reply.Engine,
		EngineName: reply.EngineName,
		Platform:   reply.Platform,
		Cache:      reply.Cache,
		Push:       req.Push,
		NoCache:    req.NoCache,
		Jobs:       int(reply.Jobs),
		PodmanJobs: int(reply.PodmanJobs),
		Dir:        req.Dir,
	}

	// Render Containerfiles via deploykit.Generator (the render DRIVE, #67).
	containerfiles, err := renderContainerfiles(ctx, ex, reply, req.Dir, req.DevLocalPkg)
	if err != nil {
		return nil, err
	}

	boxByName := make(map[string]spec.BuildResolveBox, len(reply.Boxes))
	for _, b := range reply.Boxes {
		boxByName[b.Name] = b
	}

	builtBoxes, err := cfg.buildImages(ctx, ex, reply, boxByName, containerfiles)
	if err != nil {
		return nil, err
	}

	// Push after merge (Podman only; Docker buildx pushes during build).
	if cfg.Push && cfg.EngineName == "podman" {
		fmt.Fprintf(os.Stderr, "\n=== Pushing images ===\n")
		for _, box := range builtBoxes {
			if err := pushImage(cfg.Dir, imageTags(box)); err != nil {
				return nil, err
			}
		}
	}

	built := make([]string, 0, len(builtBoxes))
	for _, box := range builtBoxes {
		built = append(built, box.FullTag)
	}
	return built, nil
}

// runBoxGenerate is the candy-side DRIVE behind the build:generate word: it asks the host
// to prep + resolve the project (build-prep), then renders the .build/ Containerfile tree
// itself via deploykit.Generator + returns the written Containerfile paths. No podman, no
// merge — the generate path builds nothing (#67 render-DRIVE move).
func runBoxGenerate(ctx context.Context, ex *sdk.Executor, req spec.BuildRequest) ([]string, error) {
	reply, err := resolveBuild(ctx, ex, req, true)
	if err != nil {
		return nil, err
	}
	// Render Containerfiles via deploykit.Generator (the render DRIVE).
	containerfiles, err := renderContainerfiles(ctx, ex, reply, req.Dir, req.DevLocalPkg)
	if err != nil {
		return nil, err
	}
	// Return the written Containerfile paths (sorted for determinism).
	written := make([]string, 0, len(containerfiles))
	for name := range containerfiles {
		written = append(written, filepath.Join(req.Dir, ".build", name, "Containerfile"))
	}
	sort.Strings(written)
	return written, nil
}

// buildImages runs the build-order loop over the host-resolved drive-model. A filtered (named)
// selection (reply.Order non-empty) builds sequentially in dependency order; a full build
// (reply.Levels) uses level-based parallelism bounded by cfg.Jobs, merging each level before the
// next so children start from a merged (fewer-layer) base image. Returns the built box descriptors
// in build order (the caller derives FullTags + the push list). Mirrors the former host-side
// BuildCmd.buildImages branch-for-branch.
func (c driveConfig) buildImages(ctx context.Context, ex *sdk.Executor, reply spec.BuildResolveReply, boxByName map[string]spec.BuildResolveBox, containerfiles map[string]string) ([]spec.BuildResolveBox, error) {
	var built []spec.BuildResolveBox

	if len(reply.Order) > 0 {
		// Filtered build: sequential dependency order.
		for _, name := range reply.Order {
			box := boxByName[name]
			if err := c.buildImage(box, containerfiles[name]); err != nil {
				return nil, fmt.Errorf("building %s: %w", name, err)
			}
			built = append(built, box)
			if box.MergeAuto {
				c.mergeBox(ctx, ex, box)
			}
		}
		return built, nil
	}

	// Full build: level-based parallelism.
	jobs := c.Jobs
	if jobs < 1 {
		jobs = jobsFallback
	}

	for i, level := range reply.Levels {
		fmt.Fprintf(os.Stderr, "\n=== Build level %d/%d (%d images) ===\n", i+1, len(reply.Levels), len(level))

		if len(level) == 1 {
			// Single image, no need for goroutine overhead.
			name := level[0]
			box := boxByName[name]
			if err := c.buildImage(box, containerfiles[name]); err != nil {
				return nil, fmt.Errorf("building %s: %w", name, err)
			}
		} else {
			g, _ := errgroup.WithContext(ctx)
			g.SetLimit(jobs)

			for _, name := range level {
				box := boxByName[name]
				g.Go(func() error {
					if err := c.buildImage(box, containerfiles[name]); err != nil {
						return fmt.Errorf("building %s: %w", name, err)
					}
					return nil
				})
			}

			if err := g.Wait(); err != nil {
				return nil, err
			}
		}

		// Merge this level before building the next so children start from a merged
		// (fewer-layer) base image.
		for _, name := range level {
			box := boxByName[name]
			if box.MergeAuto {
				c.mergeBox(ctx, ex, box)
			}
			built = append(built, box)
		}
	}
	return built, nil
}

// mergeBox gates the post-build inline layer merge on the box's MergeAuto and, when set, asks
// verb:oci to run it via InvokeProvider (the F10 peer-dispatch leg — the go-containerregistry
// merge engine lives in candy/plugin-oci now). Engine is the resolved build engine; the per-box
// MaxMB/MaxTotalMB ride from the build-resolve model (box config, 0 → plugin defaults). Like the
// former mergeAfterBuild, a merge failure only WARNS to stderr — it never fails the build; the
// plugin's progress Notes are printed by the ordinary MergeCmd path, not this inline gate.
func (c driveConfig) mergeBox(ctx context.Context, ex *sdk.Executor, box spec.BuildResolveBox) {
	reqJSON, err := json.Marshal(spec.MergeRequest{
		ImageRef:   box.FullTag,
		Engine:     c.EngineName,
		MaxMB:      int(box.MergeMaxMB),
		MaxTotalMB: int(box.MergeMaxTotalMB),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: merge %s: %v\n", box.Name, err)
		return
	}
	// verb:oci is keyed by an oci_op ENV discriminator; the MergeRequest rides Params.
	envJSON, err := json.Marshal(map[string]string{"oci_op": "merge"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: merge %s: %v\n", box.Name, err)
		return
	}
	replyJSON, err := ex.InvokeProvider(ctx, "verb", "oci", sdk.OpRun, reqJSON, envJSON, sdk.InvokeProviderOpts{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: merge %s: %v\n", box.Name, err)
		return
	}
	var reply spec.MergeReply
	if err := json.Unmarshal(replyJSON, &reply); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: merge %s: %v\n", box.Name, err)
		return
	}
	for _, note := range reply.Notes {
		fmt.Fprintln(os.Stderr, note)
	}
	if reply.Error != "" {
		fmt.Fprintf(os.Stderr, "Warning: merge %s: %s\n", box.Name, reply.Error)
	}
}

// errString flattens an error to its message (empty for nil) for the reply-error convention: a
// build failure rides spec.BuildReply.Error while the RPC itself succeeds.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
