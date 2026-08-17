package box

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// hostClient is the box commands' host coupling: it reaches charly's host process over the
// reverse channel — InvokeProvider (peer plugin dispatch, for generate → build:generate, and for
// list's store-live tags → verb:retention, listImageTags), the InvokeProvider("build","project")
// envelope fetch (inspect/list — sdk.OpResolve; validate — sdk.OpValidate, #55 step3 unit 3-I,
// with the registry word sets from HostBuild("validate-word-sets") folded onto it) fetch (validate
// runs the rule ENGINE in-plugin over the reply). build runs its body in-plugin (dispatchBuild —
// InvokeProvider(build:box) + thin HostBuild seams, P8b), no reentry; pull runs the ensure-image work
// in-plugin via InvokeProvider(build:ensure); inspect's deploy-overlay formats (tunnel/bind_mounts)
// render in-plugin off the deploy overlay + the resolved-project envelope. None of these reenter
// core — the generic HostBuild("cli") reentry helper this file used to carry (list's SOLE
// caller) is DELETED (#118): `box` has no remaining core CLI reentry.
// The `new` command needs neither (kit scaffolding directly).
type hostClient struct {
	ctx  context.Context
	exec *sdk.Executor
}

// dispatchBoxCommand routes a box command word to its handler.
func dispatchBoxCommand(hc *hostClient, word string, args []string) error {
	switch word {
	case "generate":
		return dispatchGenerate(hc, args)
	case "validate":
		return dispatchValidate(hc, args)
	case "new":
		return dispatchNew(args)
	case "pull":
		return dispatchPull(hc, args)
	case "build":
		return dispatchBuild(hc, args)
	case "inspect":
		return dispatchInspect(hc, args)
	case "list":
		return dispatchList(hc, args)
	case "labels":
		return dispatchLabels(args)
	case "load":
		return dispatchLoad(args)
	case "merge":
		return dispatchMerge(hc, args)
	case "reconcile":
		return dispatchReconcile(args)
	case "feature":
		return dispatchFeature(hc, args)
	default:
		return fmt.Errorf("box: unknown command word %q", word)
	}
}

// dispatchFeature runs `charly box feature run <image>` — build-scope Agent Driven Evaluation against
// a disposable container. The ENGINE lives in candy/plugin-check (where the check runner is); this
// box command bridges to it over the F10 plugin↔plugin reverse leg (cone-C #31, the SAME shape
// command:build→build:ensure uses): InvokeProvider command:check's HIDDEN `__feature-box` leaf, which
// routes to plugin-check's Mode:"feature-box" engine. The check output prints to charly's own stdio
// (compiled-in) and the check-fail exit code propagates back through the returned error.
func dispatchFeature(hc *hostClient, args []string) error {
	if len(args) == 0 || args[0] != "run" {
		return fmt.Errorf("usage: charly box feature run <image> [--format …] [--tag …] [--strict]")
	}
	// `run <image> [flags]` → the hidden check leaf `__feature-box <image> [flags]` (drop the `run`
	// subcommand token — __feature-box takes the image positional directly).
	fwd := append([]string{"__feature-box"}, args[1:]...)
	reqJSON, err := json.Marshal(struct {
		Args []string `json:"args"`
	}{Args: fwd})
	if err != nil {
		return err
	}
	_, ierr := hc.exec.InvokeProvider(hc.ctx, "command", "check", sdk.OpRun, reqJSON, nil, sdk.InvokeProviderOpts{})
	return ierr
}

// parseLeaf kong-parses args into a single-command grammar struct (positional args + flags, no
// subcommands) via the shared sdk helper, which neutralises kong's process-exit and handles
// `--help`/`--version` cleanly. done=true means kong printed help/version — the caller MUST return
// nil without running the leaf's action (otherwise `charly box <leaf> --help` would run the leaf).
func parseLeaf(name string, target any, args []string) (done bool, err error) {
	return sdk.ParseInProcCLI("box "+name, target, args)
}

// --- box generate ---

// generateGrammar is the `charly box generate [boxes…] [--tag] [--include-disabled]` CLI surface.
type generateGrammar struct {
	Boxes           []string `arg:"" optional:"" help:"Boxes to generate (default: all enabled). The sentinel 'all' is equivalent to passing no argument."`
	Tag             string   `name:"tag" help:"Override tag (default: CalVer)"`
	IncludeDisabled bool     `name:"include-disabled" help:"Generate boxes with enabled: false in charly.yml (does not modify the file). Scoped to the named boxes when any are given."`
}

// dispatchGenerate renders the .build/ Containerfile tree by INVOKING the peer COMPILED-IN
// build:generate word (candy/plugin-build) over the InvokeProvider reverse leg — the SAME path the
// former core dispatchBoxGenerate took (invoke build:generate with OpBuild), so build:generate stays
// the single generate implementation (no duplication, no orphaned capability). The host build-resolve
// seam normalizes the `all` sentinel + scopes the selection, so the boxes ride verbatim.
func dispatchGenerate(hc *hostClient, args []string) error {
	var g generateGrammar
	if done, err := parseLeaf("generate", &g, args); err != nil || done {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	reqJSON, err := json.Marshal(spec.BuildRequest{
		Boxes:           g.Boxes,
		Tag:             g.Tag,
		Dir:             dir,
		IncludeDisabled: g.IncludeDisabled,
	})
	if err != nil {
		return err
	}
	resJSON, err := hc.exec.InvokeProvider(hc.ctx, "build", "generate", sdk.OpBuild, reqJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	var reply spec.BuildReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return fmt.Errorf("box generate: decode reply: %w", err)
		}
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}

// --- box validate ---

// validateGrammar is the `charly box validate [--include-disabled]` CLI surface. The validate ENGINE
// itself lives in validate.go (it reads the resolved-project envelope + re-runs the deploykit graph
// checks); dispatchValidate is defined there.
type validateGrammar struct {
	IncludeDisabled bool `name:"include-disabled" help:"Include boxes with enabled: false in validation (does not modify charly.yml)"`
}

// --- box pull ---

// pullGrammar is the `charly box pull <box> [--tag] [--platform]` CLI surface — byte-identical to
// the former static BoxPullCmd Kong leaf (FINAL/K5 unit 6a M4c): same positional, same two flags,
// same help text, so `charly box pull --help` renders unchanged.
type pullGrammar struct {
	Box      string `arg:"" help:"Box name (short, resolved via charly.yml), fully-qualified ref, or @github.com/org/repo/box[:version]"`
	Tag      string `name:"tag" help:"Image CalVer tag when resolving a short name (empty = resolve from charly.yml metadata or error with explicit guidance)"`
	Platform string `name:"platform" help:"Target platform (default: host)"`
}

// dispatchPull ensures an image is present in local storage by INVOKING the peer COMPILED-IN
// build:ensure word (candy/plugin-build) over the InvokeProvider reverse leg — the SAME
// ensure-image ORCHESTRATION (pull from registry, fall back to a local/remote build when the
// identifier maps to a project charly.yml entry) the former core BoxPullCmd.Run delegated to via
// dispatchBuildEnsure, now reached plugin↔plugin (the hidden __box-pull core reentry is DELETED).
//
// A --tag override is meaningful ONLY for a short-name input: resolve the canonical registry ref
// (registry+name from the resolved-project envelope the plugin already reads + the requested tag)
// so build:ensure's pull/build-fallback picks up the requested tag — byte-identical to the former
// core Run's `buildkit.ResolveBox(cfg,box,tag).Registry/.Name` → ResolveShellImageRef path, but off
// the envelope (registry/name are tag-independent), so no loader is needed plugin-side. A
// full/remote ref already carries its own tag. --platform stays a no-op (the former Run never
// threaded it to the ensure drive either).
func dispatchPull(hc *hostClient, args []string) error {
	var g pullGrammar
	if done, err := parseLeaf("pull", &g, args); err != nil || done {
		return err
	}
	dir, _ := os.Getwd()
	image := g.Box
	if g.Tag != "" && !kit.LooksLikeFullRef(g.Box) && !spec.IsRemoteImageRef(kit.StripURLScheme(g.Box)) {
		rp, err := hc.resolvedProject(false)
		if err != nil {
			return fmt.Errorf("short name %q with --tag requires a project directory with charly.yml: %w", g.Box, err)
		}
		view, ok := rp.Boxes[g.Box]
		if !ok {
			return fmt.Errorf("short name %q with --tag not found in charly.yml", g.Box)
		}
		image = kit.ResolveShellImageRef(view.Registry, view.Name, g.Tag)
	}
	reqJSON, err := json.Marshal(spec.BuildEnsureRequest{Image: image, Dir: dir})
	if err != nil {
		return err
	}
	resJSON, err := hc.exec.InvokeProvider(hc.ctx, "build", "ensure", sdk.OpBuild, reqJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	var reply spec.BuildEnsureReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return fmt.Errorf("box pull: decode reply: %w", err)
		}
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}

// --- box build ---

// buildGrammar is the `charly box build [boxes…] [flags]` CLI surface — byte-identical to the
// former static core build leaf (P8b relocated its Run body into dispatchBuild): same positional,
// same nine flags (including the three env-var-backed tunables), same help text, so
// `charly box build --help` renders unchanged and CHARLY_BUILD_CACHE/CHARLY_BUILD_JOBS/
// CHARLY_PODMAN_JOBS keep working — Kong resolves them in-plugin at parse time and dispatchBuild
// threads the resolved values into the spec.BuildRequest it invokes build:box with.
type buildGrammar struct {
	Boxes           []string `arg:"" optional:"" help:"Boxes to build (default: all enabled; the sentinel 'all' is equivalent). Supports remote refs (github.com/org/repo/box[@version])"`
	Push            bool     `name:"push" help:"Push to registry after building"`
	Tag             string   `name:"tag" help:"Override tag (default: CalVer)"`
	Platform        string   `name:"platform" help:"Target platform (default: host platform)"`
	Cache           string   `name:"cache" help:"Build cache type: registry, image, gha, none (default: auto)" env:"CHARLY_BUILD_CACHE"`
	NoCache         bool     `name:"no-cache" help:"Disable build cache entirely"`
	Jobs            int      `name:"jobs" help:"Max concurrent image builds per DAG level (0=auto: defaults.jobs, else 4)" env:"CHARLY_BUILD_JOBS"`
	PodmanJobs      int      `name:"podman-jobs" help:"Stages per podman build (0=auto: min(NCPU, defaults.podman_jobs_cap))" env:"CHARLY_PODMAN_JOBS"`
	IncludeDisabled bool     `name:"include-disabled" help:"Build boxes with enabled: false in charly.yml (does not modify the file). Use for one-off operational rebuilds without flipping authored config."`
	DevLocalPkg     bool     `name:"dev-local-pkg" help:"Build localpkg candies (the charly toolchain) from LOCAL in-development source instead of downloading the published release. Set automatically for disposable check-bed image builds so a bed tests in-development code; never on a production box build."`
}

// dispatchBuild runs the `charly box build` body IN-PLUGIN (P8b — the former hidden core
// __box-build reentry is DELETED): NormalizeBoxArgs → remote-ref pivot DETECTION (pure sdk,
// buildkit.DetectRemoteBuildRef) → resolve any remote ref over the existing
// HostBuild("remote-image-resolve") seam → compute the CalVer tag ONCE → hold the build-activity
// flock → InvokeProvider(build:box) (the compiled-in candy/plugin-build podman DRIVE) → post-build
// retention prune (skipped for --push). The host-coupled remainder a sdk-only candy cannot do — the
// remote-ref clone/cache (EnsureRepoDownloaded, K1) — is reached over the thin HostBuild seam
// (remote-image-resolve); keep_images resolves PLUGIN-SIDE via loaderkit.ResolveRetentionDefaultsViaExecutor
// (K-wave 2 cone R6 — the former retention-defaults seam is DELETED). Byte-equivalent to the
// former BuildCmd.Run.
func dispatchBuild(hc *hostClient, args []string) error {
	var g buildGrammar
	if done, err := parseLeaf("build", &g, args); err != nil || done {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	// Normalize the `all` sentinel to nil BEFORE any per-name interpretation (remote-ref pivot,
	// the resolver) so every surface agrees "no specific boxes" means "all enabled".
	boxes := buildkit.NormalizeBoxArgs(g.Boxes)

	// Compute the build tag ONCE (clock-derived — resolving it twice would diverge) so the
	// activity-lock floor and the built images agree on ONE CalVer.
	tag := g.Tag
	if tag == "" {
		tag = buildkit.ComputeCalVer()
	}

	req := spec.BuildRequest{
		Boxes:           boxes,
		Tag:             tag,
		Dir:             dir,
		IncludeDisabled: g.IncludeDisabled,
		DevLocalPkg:     g.DevLocalPkg,
		Push:            g.Push,
		Platform:        g.Platform,
		Cache:           g.Cache,
		NoCache:         g.NoCache,
		Jobs:            g.Jobs,
		PodmanJobs:      g.PodmanJobs,
	}

	// Remote-ref pivot: detection is pure (a box arg that is itself a remote @ref, or a thin
	// workspace whose sole import auto-pivots a locally-undeclared image to its upstream source);
	// the K1-coupled RESOLUTION (clone/cache the source, EnsureRepoDownloaded) rides the existing
	// "remote-image-resolve" host seam. On a hit, reset to a fresh single-box build against the
	// cached source dir — byte-equivalent to the former buildRemote's `(&BuildCmd{Boxes,Tag}).Run()`,
	// which dropped every other flag (push/platform/cache/jobs/include-disabled/dev-local-pkg).
	if remoteRef, ok := buildkit.DetectRemoteBuildRef(dir, boxes); ok {
		resolved, rerr := hc.remoteImageResolve(remoteRef, tag)
		if rerr != nil {
			return rerr
		}
		req = spec.BuildRequest{Boxes: []string{resolved.BoxName}, Tag: tag, Dir: resolved.CacheDir}
	}

	// Retention floor: mark this build LIVE so a concurrent sibling's retention prune respects our
	// tag floor. Held across the whole build (the candy podman drive) + the post-build prune.
	release, err := acquireBuildActivityLock(tag)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()

	// The podman DRIVE runs in the compiled-in candy/plugin-build (build:box), reached over the
	// InvokeProvider reverse leg — the SAME peer-invoke dispatchGenerate uses for build:generate.
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return err
	}
	resJSON, err := hc.exec.InvokeProvider(hc.ctx, "build", "box", sdk.OpBuild, reqJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	var reply spec.BuildReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return fmt.Errorf("box build: decode reply: %w", uerr)
		}
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}

	// Reusable-artifact retention (post-step; skipped for --push): prune old CalVer tags + stale
	// .build/_candy dirs down to defaults.keep_images, via verb:retention, under the lock held above.
	if !req.Push {
		pruneAfterBuild(hc, req.Dir)
	}
	return nil
}

// remoteImageResolve resolves a known remote @ref to its cached source dir + short box name over the
// existing "remote-image-resolve" host seam (host_build_remote_image_resolve.go → EnsureRepoDownloaded,
// the K1-coupled clone/cache the sdk-only candy cannot run; the box-RESOLVE itself runs plugin-side
// in candy/plugin-build's ensureRemoteRef) — the SAME seam candy/plugin-build's ensure-image fallback
// reaches. The build then re-dispatches build:box against the returned dir.
func (h *hostClient) remoteImageResolve(ref, tag string) (spec.RemoteImageResolveReply, error) {
	reqJSON, err := json.Marshal(spec.RemoteImageResolveRequest{Ref: ref, Tag: tag})
	if err != nil {
		return spec.RemoteImageResolveReply{}, err
	}
	resJSON, err := h.exec.HostBuild(h.ctx, "remote-image-resolve", reqJSON)
	if err != nil {
		return spec.RemoteImageResolveReply{}, err
	}
	var reply spec.RemoteImageResolveReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return spec.RemoteImageResolveReply{}, uerr
		}
	}
	if reply.Error != "" {
		return spec.RemoteImageResolveReply{}, fmt.Errorf("%s", reply.Error)
	}
	if reply.BoxName == "" {
		return spec.RemoteImageResolveReply{}, fmt.Errorf("remote-image-resolve: empty box name resolving %q", ref)
	}
	return reply, nil
}

// acquireBuildActivityLock registers this build invocation as LIVE for its whole duration: a flocked
// nonce file whose CONTENT is the build's CalVer — the floor of every FROM pin its generated
// Containerfiles carry. The externalized retention engine (candy/plugin-clean) consults the SAME live
// set (kit.BuildActivityDir) so a completing sibling build can never untag a pin an in-flight build
// still resolves. Reconstructed from the shared kit primitives — byte-equivalent to the former core
// acquireBuildActivityLock (deleted with charly/build.go in P8b), with no core copy remaining.
func acquireBuildActivityLock(calver string) (func() error, error) {
	dir, err := kit.BuildActivityDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("build-%d-%d.lock", os.Getpid(), time.Now().UnixNano()))
	release, err := kit.AcquireFileLock(path, true)
	if err != nil {
		return nil, fmt.Errorf("build-activity lock: %w", err)
	}
	if err := os.WriteFile(path, []byte(calver+"\n"), 0o644); err != nil {
		_ = release()
		return nil, fmt.Errorf("build-activity lock: record calver: %w", err)
	}
	return func() error {
		rerr := release()
		_ = os.Remove(path)
		return rerr
	}, nil
}

// pruneAfterBuild runs the post-build retention prune via verb:retention (BuildPrune scope: tag
// retention + stale .build/_candy staging dirs). Best-effort, warn-only. keep_images resolves
// PLUGIN-SIDE via the shared sdk/loaderkit.ResolveRetentionDefaultsViaExecutor (K-wave 2 cone R6 —
// the former "retention-defaults" HostBuild seam is DELETED) — the SAME defaults resolution
// `charly clean` and candy/plugin-check's post-run prune hook use. Byte-equivalent to the former
// core pruneAfterBuild (deleted from charly/retention_plugin.go in P8b).
func pruneAfterBuild(hc *hostClient, dir string) {
	keep, _ := loaderkit.ResolveRetentionDefaultsViaExecutor(hc.ctx, hc.exec, dir)
	reqJSON, err := json.Marshal(spec.RetentionRequest{Dir: dir, BuildPrune: true, KeepImages: keep})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: image retention prune: %v\n", err)
		return
	}
	resJSON, err := hc.exec.InvokeProvider(hc.ctx, "verb", "retention", sdk.OpRun, reqJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: image retention prune: %v\n", err)
		return
	}
	var reply spec.RetentionReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			fmt.Fprintf(os.Stderr, "Warning: image retention prune: %v\n", uerr)
			return
		}
	}
	if reply.Error != "" {
		fmt.Fprintf(os.Stderr, "Warning: image retention prune: %s\n", reply.Error)
		return
	}
	if len(reply.ImageRefs) > 0 {
		fmt.Fprintf(os.Stderr, "Pruned %d old image tag(s) (keep_images=%d)\n", len(reply.ImageRefs), keep)
	}
	if len(reply.BuildDirs) > 0 {
		fmt.Fprintf(os.Stderr, "Pruned %d build-staging dir(s) under .build/_candy\n", len(reply.BuildDirs))
	}
}

// --- box labels ---

// labelsGrammar is the `charly box labels <image> [--format] [--all]` CLI surface.
type labelsGrammar struct {
	Image  string `arg:"" help:"Image reference: a full ref, '<box>:<calver>' to pin one build, or a bare short name resolved against local container storage (refused when a newer local build of that box exists); never reads charly.yml"`
	Format string `name:"format" help:"Print only this label's raw value — a full key, or the ai.opencharly.<key> shorthand (e.g. 'init'); exits non-zero when the label is absent"`
	All    bool   `name:"all" help:"Print every label, not just the ai.opencharly.* contract"`
}

// dispatchLabels prints a built image's OCI labels straight from local container storage — pure
// container-storage probes (kit.ResolveRuntime/ResolveLocalImageRef/InspectImageLabels), zero
// loader coupling, zero host reentry (K3 reentry-class dissolution — this word no longer needs
// hc at all, matching the `new` group's zero-reentry pattern).
func dispatchLabels(args []string) error {
	var g labelsGrammar
	if done, err := parseLeaf("labels", &g, args); err != nil || done {
		return err
	}
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}
	// `charly box labels` is the charly-native R8 artifact check — a verdict on a built artifact.
	// Reporting an older image's labels as the fresh build's is the same false-proof class as a
	// stale `charly check box`, so it resolves through the guarded form.
	imageRef, err := kit.ResolveBuiltImageRef(rt.RunEngine, g.Image)
	if err != nil {
		return err
	}
	// Provenance FIRST, on every path — the same rule `charly check box` follows. This verb is the
	// charly-native R8 artifact check, so a reader must be able to tell WHICH image the labels came
	// from; without the line, `box labels <short-name>` reports a capability contract with no way to
	// audit which artifact it read. It goes to STDERR so `--format <key>`'s single raw stdout value
	// (the scripting contract every plan step pipes into grep) is byte-unchanged.
	fmt.Fprintf(os.Stderr, "Image: %s\n", imageRef)
	labels, err := kit.InspectImageLabels(rt.RunEngine, imageRef)
	if err != nil {
		if !kit.LocalImageExists(rt.RunEngine, imageRef) {
			return fmt.Errorf("%w: %s", kit.ErrImageNotLocal, imageRef)
		}
		return err
	}
	if g.Format != "" {
		key := canonicalLabelKey(g.Format)
		v, ok := labels[key]
		if !ok {
			return fmt.Errorf("label %q not present on %s — an empty or missing capability label is a failure (CLAUDE.md R8)", key, imageRef)
		}
		fmt.Println(v)
		return nil
	}
	keys := sortedLabelKeys(labels, g.All)
	if len(keys) == 0 {
		return fmt.Errorf("no %s labels on %s — not an opencharly image (use --all for every label)", "ai.opencharly.*", imageRef)
	}
	for _, k := range keys {
		fmt.Printf("%s=%s\n", k, labels[k])
	}
	return nil
}

// canonicalLabelKey expands the ai.opencharly.<key> shorthand: a bare token without dots refers
// to the capability-contract namespace. Moved from charly/box_labels_cmd.go (K3 reentry-class
// dissolution).
func canonicalLabelKey(k string) string {
	if strings.Contains(k, ".") {
		return k
	}
	return "ai.opencharly." + k
}

// sortedLabelKeys returns the label keys to print, sorted; without --all only the
// ai.opencharly.* contract participates. Moved from charly/box_labels_cmd.go (K3 reentry-class
// dissolution).
func sortedLabelKeys(labels map[string]string, all bool) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if !all && !strings.HasPrefix(k, "ai.opencharly.") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- box new (candy/project/box) ---

// newGrammar is the `charly box new candy/project/box …` subcommand group. Each leaf's Run calls the
// sdk/kit scaffold ENGINE directly (kit.ScaffoldCandy / kit.ScaffoldProject / kit.AddBox), so the
// whole `new` group externalizes with ZERO core reentry — the scaffolders already live in sdk/kit.
type newGrammar struct {
	Candy   newCandyGrammar   `cmd:"" name:"candy" help:"Scaffold a candy directory"`
	Project newProjectGrammar `cmd:"" name:"project" help:"Scaffold a fresh charly project (charly.yml + candy/)"`
	Box     newBoxGrammar     `cmd:"" name:"box" help:"Add a new box entry to charly.yml"`
}

// dispatchNew kong-parses the `new` subcommand tree and runs the selected leaf (kctx.Run dispatches
// to that leaf's Run — none needs the host reverse channel).
func dispatchNew(args []string) error {
	var g newGrammar
	return sdk.RunInProcCLI("box new", &g, args)
}

// nowCalVer computes the current wall-clock CalVer via the existing kit.CalVer type (no duplicate
// format literal): the candy-identity stamp ScaffoldCandy writes into the new candy's charly.yml.
func nowCalVer() string {
	now := time.Now().UTC()
	return kit.CalVer{Year: now.Year(), Day: now.YearDay(), HHMM: now.Hour()*100 + now.Minute()}.String()
}

type newCandyGrammar struct {
	Name string `arg:"" help:"Candy name"`
}

func (c *newCandyGrammar) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := kit.ScaffoldCandy(dir, c.Name, nowCalVer()); err != nil {
		return err
	}
	fmt.Printf("Created candy at %s\n", filepath.Join(dir, kit.DefaultCandyDir, c.Name))
	fmt.Println("Files created:")
	fmt.Println("  charly.yml - Candy config (distro packages, require, env, port, route, service)")
	fmt.Println()
	fmt.Println("Optional files you can add:")
	fmt.Println("  root.yml        - Custom root install task")
	fmt.Println("  pixi.toml       - Python/conda packages")
	fmt.Println("  package.json    - npm packages")
	fmt.Println("  Cargo.toml      - Rust crate (requires src/)")
	fmt.Println("  user.yml        - Custom user install task")
	return nil
}

type newProjectGrammar struct {
	Dir string `arg:"" help:"Directory to scaffold the project in (created if missing)"`
}

func (c *newProjectGrammar) Run() error {
	if err := kit.ScaffoldProject(c.Dir); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Scaffolded project at %s\n", c.Dir)
	fmt.Fprintln(os.Stderr, "Next steps:")
	fmt.Fprintln(os.Stderr, "  # The distro/builder/init build vocabulary is embedded — declare distro:/builder:/init: only to override it.")
	fmt.Fprintln(os.Stderr, "  # Add a candy, populate it, wire it into a box, then build:")
	fmt.Fprintln(os.Stderr, "  charly -C "+c.Dir+" box new candy my-candy")
	fmt.Fprintln(os.Stderr, "  charly -C "+c.Dir+" candy add-rpm my-candy curl jq")
	fmt.Fprintln(os.Stderr, "  charly -C "+c.Dir+" box new box my-box --base quay.io/fedora/fedora:43 --candy my-candy")
	fmt.Fprintln(os.Stderr, "  charly -C "+c.Dir+" box build my-box")
	return nil
}

type newBoxGrammar struct {
	Name    string   `arg:"" help:"Name for the new box entry"`
	Base    string   `name:"base" required:"" help:"Base image (URL like quay.io/... or another box name)"`
	Candies []string `name:"candy" sep:"," help:"Comma-separated list of candy names to include"`
}

func (c *newBoxGrammar) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := kit.AddBox(dir, c.Name, c.Base, c.Candies); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Added box %s to charly.yml\n", c.Name)
	return nil
}
