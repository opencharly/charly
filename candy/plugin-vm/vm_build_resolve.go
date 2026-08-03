package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// vm_build_resolve.go — the `charly vm build` PREP+RESOLVE, run PLUGIN-SIDE (K3 vm-build move,
// coneB-buildremnant — the former hidden charly/host_build_vm_build.go HostBuild("vm-build") reentry
// is DELETED). Every dependency the host seam used to justify staying core is now plugin-reachable:
// the kind:vm entity load goes through loaderkit.LoadUnifiedViaExecutor (the SAME hoisted K1-loader
// seam candy/plugin-build's build-engine resolve uses, K3-W2) + the vm template projection over
// InvokeProvider(kind, local, OpResolve) (the
// SAME substrate-template-resolve leg charly/substrate_template_resolve.go's resolveVmViaPlugin
// already dispatches to — command:vm reaches it itself instead of round-tripping through core); the
// distro/builder build vocabulary goes through loaderkit.ProjectDistroConfig/ProjectBuilderConfig
// (mirroring charly/format_config.go's LoadBuildConfigForBox) with a resolveDistroLeg-equivalent
// InvokeProvider(kind, distro, OpResolve) closure (R3 — the same small callback
// candy/plugin-build/resolve_legs.go carries, duplicated per-module since separate Go modules cannot
// share package-private helpers); the on-demand builder-image auto-build goes through
// InvokeProvider(build, box, OpBuild) (the SAME compiled-in build:box word candy/plugin-box's
// dispatchBuild already drives, mirroring build.go's dispatchBuild — the pattern that ALSO backs
// candy/plugin-build's own build:ensure fallback); resolveBootcImageRef + vmDir + the deploy-state
// read are already 100% pure sdk (kit.ResolveLocalImageRef, vmshared.VmStateRoot,
// loaderkit.LoadHostBundleConfigViaExecutor) with zero core coupling.

// knownVmSourceKinds lists the source.kind values `charly vm build` supports. Used by the
// unsupported-kind error message so adding a new kind keeps the enumeration in sync with the switch.
var knownVmSourceKinds = []string{"cloud_image", "bootc", "bootstrap"}

// noVmEntityErr is the shared "no kind:vm entity" error both entity-lookup failure paths raise.
func noVmEntityErr(boxName string) error {
	return fmt.Errorf(
		"VM %q has no kind:vm entity in vm.yml.\n"+
			"  For a bootc VM, declare one in vm.yml:\n"+
			"      vm:\n"+
			"        %s-bootc:\n"+
			"          source:\n"+
			"            kind: bootc\n"+
			"            image: %s\n"+
			"          disk_size: 20G",
		boxName, boxName, boxName)
}

// resolveDistroLeg is the plugin-side InvokeProvider(kind, distro, OpResolve) callback loaderkit's
// vocabulary projection needs to decode an opaque distro: body — R3 duplicate of
// candy/plugin-build/resolve_legs.go's resolveDistroLeg (a separate Go module; small pure closure).
func resolveDistroLeg(ctx context.Context, ex *sdk.Executor) func(json.RawMessage) (*spec.ResolvedDistro, error) {
	return func(body json.RawMessage) (*spec.ResolvedDistro, error) {
		params, err := json.Marshal(spec.DistroResolveInput{Distro: body})
		if err != nil {
			return nil, err
		}
		res, err := ex.InvokeProvider(ctx, "kind", "distro", sdk.OpResolve, params, nil, sdk.InvokeProviderOpts{})
		if err != nil {
			return nil, err
		}
		var reply spec.DistroResolveReply
		if len(res) > 0 {
			if err := json.Unmarshal(res, &reply); err != nil {
				return nil, fmt.Errorf("distro resolve: decode reply: %w", err)
			}
		}
		return reply.Resolved, nil
	}
}

// loadVmProjectUnified loads the project at dir plugin-side via loaderkit.LoadUnifiedViaExecutor —
// the SAME hoisted K1-loader seam infra candy/plugin-build's resolveBuildEngine uses (K3-W2: the
// per-candy vmLoaderExecutor copy was hoisted into loaderkit as the ONE canonical
// executorLoaderExecutor, R3).
func loadVmProjectUnified(ctx context.Context, ex *sdk.Executor, dir string) (*spec.UnifiedFile, bool, error) {
	return loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir)
}

// resolveVmBuildEntity loads the project + resolves boxName's kind:vm entity into a *VmSpec, entirely
// plugin-side: LoadUnified (above) finds the opaque vm: body, then InvokeProvider(kind, local,
// OpResolve) projects it — the SAME substrate-template-resolve leg
// charly/substrate_template_resolve.go's resolveVmViaPlugin dispatches to (any compiled-in plugin can
// reach it directly; command:vm no longer needs to round-trip through core for this).
func resolveVmBuildEntity(ctx context.Context, ex *sdk.Executor, dir, boxName string) (*VmSpec, error) {
	uf, ok, err := loadVmProjectUnified(ctx, ex, dir)
	if err != nil || !ok || uf.VM() == nil {
		return nil, noVmEntityErr(boxName)
	}
	body, hit := uf.VM()[boxName]
	if !hit {
		return nil, noVmEntityErr(boxName)
	}
	if len(body) == 0 {
		return nil, nil
	}
	params, err := json.Marshal(spec.SubstrateTemplateResolveRequest{Vm: &spec.VmResolveInput{Vm: body}})
	if err != nil {
		return nil, err
	}
	res, err := ex.InvokeProvider(ctx, "kind", "local", sdk.OpResolve, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return nil, err
	}
	var reply spec.VmResolveReply
	if len(res) > 0 {
		if err := json.Unmarshal(res, &reply); err != nil {
			return nil, fmt.Errorf("vm resolve: decode reply: %w", err)
		}
	}
	return reply.Resolved, nil
}

// loadVmBuildYmlSections loads the distro: + builder: blocks of the embedded build vocabulary
// plugin-side (mirrors charly/format_config.go's LoadBuildConfigForBox, minus the Init projection
// vm-build never needs): LoadUnified + loaderkit.ProjectDistroConfig(resolveDistroLeg) +
// loaderkit.ProjectBuilderConfig (pure, no callback).
func loadVmBuildYmlSections(ctx context.Context, ex *sdk.Executor, dir string) (*spec.DistroConfig, *spec.BuilderConfig, error) {
	uf, ok, err := loadVmProjectUnified(ctx, ex, dir)
	if err != nil {
		return nil, nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !ok {
		return nil, nil, fmt.Errorf("no charly.yml found at %s", dir)
	}
	return loaderkit.ProjectDistroConfig(uf, resolveDistroLeg(ctx, ex)), loaderkit.ProjectBuilderConfig(uf), nil
}

// resolveVmBuildBootstrap resolves the distro/builder vocabulary + pre-builds the builder image for a
// "bootstrap" source.kind, filling reply.{BuilderImageRef,DistroJSON,BuilderJSON}.
func resolveVmBuildBootstrap(ctx context.Context, ex *sdk.Executor, dir, engine string, vmSpec *VmSpec, reply *spec.VmBuildReply) error {
	distroCfg, builderCfg, lerr := loadVmBuildYmlSections(ctx, ex, dir)
	if lerr != nil {
		return fmt.Errorf("loading builder/distro sections from the embedded build vocabulary: %w", lerr)
	}
	if builderCfg == nil || builderCfg.Builder == nil {
		return fmt.Errorf("the builder: section of the embedded vocabulary (charly/charly.yml) is empty; cannot resolve %q", vmSpec.Source.Builder)
	}
	builder, ok := builderCfg.Builder[vmSpec.Source.Builder]
	if !ok {
		return fmt.Errorf("builder %q not declared in the embedded build vocabulary (charly/charly.yml)", vmSpec.Source.Builder)
	}
	if !builder.IsBootstrap() {
		return fmt.Errorf("builder %q is not kind: bootstrap", vmSpec.Source.Builder)
	}
	if distroCfg == nil {
		return fmt.Errorf("the distro: section of the embedded vocabulary (charly/charly.yml) is empty; cannot resolve %q", vmSpec.Source.Distro)
	}
	distro, ok := distroCfg.Distro[vmSpec.Source.Distro]
	if !ok {
		return fmt.Errorf("distro %q not declared in the embedded build vocabulary (charly/charly.yml)", vmSpec.Source.Distro)
	}
	distro = distroCfg.ResolveInherits(distro, 10)
	if distro.Bootloader == nil {
		return fmt.Errorf("distro %q has no bootloader: block in the embedded build vocabulary (charly/charly.yml) (required for VM bootstrap)", vmSpec.Source.Distro)
	}
	if vmSpec.Source.BuilderImage == "" {
		return fmt.Errorf("source.builder_image is required for bootstrap VMs")
	}
	if vmSpec.DiskSize == "" {
		return fmt.Errorf("disk_size is required for bootstrap VMs")
	}
	builderRef, berr := ensureBuilderImageBuilt(ctx, ex, engine, vmSpec.Source.BuilderImage)
	if berr != nil {
		return berr
	}
	distroJSON, jerr := json.Marshal(distro)
	if jerr != nil {
		return fmt.Errorf("marshalling resolved distro: %w", jerr)
	}
	builderJSON, jerr := json.Marshal(builder)
	if jerr != nil {
		return fmt.Errorf("marshalling resolved builder: %w", jerr)
	}
	reply.BuilderImageRef = builderRef
	reply.DistroJSON = distroJSON
	reply.BuilderJSON = builderJSON
	return nil
}

// resolveBootcImageRef maps a bootc source.image to a concrete OCI ref. 100% pure sdk (zero core
// coupling): a full ref (containing "/") passes through unchanged; an internal kind:image short name
// resolves against local podman storage to its newest CalVer tag via kit.ResolveLocalImageRef —
// charly is CalVer-only, so there is NO `:latest` fallback.
func resolveBootcImageRef(engine, image string) (string, error) {
	if strings.Contains(image, "/") {
		return image, nil
	}
	resolved, err := kit.ResolveLocalImageRef(engine, image)
	if err != nil {
		return "", fmt.Errorf("resolving bootc image %q: %w (build it first with `charly box build %s`)", image, err, image)
	}
	return resolved, nil
}

// ensureBuilderImageBuilt resolves an internal builder-image name to its newest local CalVer tag,
// BUILDING it on demand when it isn't in local storage via InvokeProvider(build, box, OpBuild) — the
// SAME compiled-in build:box word candy/plugin-box's dispatchBuild drives for `charly box build`,
// reached directly instead of round-tripping through a core reentry. A ref containing "/" (a full
// registry ref) is returned unchanged.
func ensureBuilderImageBuilt(ctx context.Context, ex *sdk.Executor, engine, builderRef string) (string, error) {
	if strings.Contains(builderRef, "/") {
		return builderRef, nil
	}
	if resolved, err := kit.ResolveLocalImageRef(engine, builderRef); err == nil {
		return resolved, nil
	}
	fmt.Fprintf(os.Stderr, "Builder image %q not in local storage — building it automatically...\n", builderRef)
	params, err := json.Marshal(spec.BuildRequest{Boxes: []string{builderRef}, IncludeDisabled: true})
	if err != nil {
		return "", err
	}
	res, err := ex.InvokeProvider(ctx, "build", "box", sdk.OpBuild, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return "", fmt.Errorf("auto-building builder image %q: %w", builderRef, err)
	}
	var reply spec.BuildReply
	if len(res) > 0 {
		if uerr := json.Unmarshal(res, &reply); uerr != nil {
			return "", fmt.Errorf("auto-building builder image %q: decode reply: %w", builderRef, uerr)
		}
	}
	if reply.Error != "" {
		return "", fmt.Errorf("auto-building builder image %q: %s", builderRef, reply.Error)
	}
	resolved, err := kit.ResolveLocalImageRef(engine, builderRef)
	if err != nil {
		return "", fmt.Errorf("builder image %q still not found after auto-build: %w", builderRef, err)
	}
	return resolved, nil
}

// parseImageArg splits "image:tag" into (image, tag). If no colon, tag is empty.
func parseImageArg(arg string) (string, string) {
	if i := strings.LastIndex(arg, ":"); i > 0 {
		return arg[:i], arg[i+1:]
	}
	return arg, ""
}

// resolveVmBuild runs the FULL `charly vm build` PREP+RESOLVE plugin-side, replacing the former
// HostBuild("vm-build") reentry body byte-for-byte (charly/host_build_vm_build.go's hostBuildVmBuild,
// DELETED).
func resolveVmBuild(ctx context.Context, ex *sdk.Executor, req spec.VmBuildRequest) (spec.VmBuildReply, error) {
	dir, err := os.Getwd()
	if err != nil {
		return spec.VmBuildReply{}, err
	}

	boxName, imageTag := parseImageArg(req.Box)
	_ = imageTag // reserved for a future box-tag pin on the disk-build path; unused today, as before

	vmSpec, err := resolveVmBuildEntity(ctx, ex, dir, boxName)
	if err != nil {
		return spec.VmBuildReply{}, err
	}

	rt, rtErr := kit.ResolveRuntime()
	if rtErr != nil {
		return spec.VmBuildReply{}, rtErr
	}
	engine := "podman"
	if rt != nil {
		engine = kit.EngineBinary(rt.RunEngine)
	}

	outputDir, err := filepath.Abs(vmshared.VmDiskDir(boxName))
	if err != nil {
		return spec.VmBuildReply{}, err
	}
	vmStateBase, err := vmshared.VmStateRoot()
	if err != nil {
		return spec.VmBuildReply{}, err
	}
	vmStateDir := filepath.Join(vmStateBase, "charly-"+boxName)
	if err := os.MkdirAll(vmStateDir, 0o755); err != nil {
		return spec.VmBuildReply{}, err
	}

	var existingState *spec.VmDeployState
	if dc, derr := loaderkit.LoadHostBundleConfigViaExecutor(ctx, ex); derr == nil && dc != nil {
		if e, ok := dc.LookupKey("vm:" + boxName); ok {
			existingState = e.VmState
		}
	}

	vmJSON, err := json.Marshal(vmSpec)
	if err != nil {
		return spec.VmBuildReply{}, fmt.Errorf("marshalling resolved vm spec: %w", err)
	}

	reply := spec.VmBuildReply{
		SourceKind:    vmSpec.Source.Kind,
		VmJSON:        vmJSON,
		Engine:        engine,
		Rootful:       rt != nil && rt.Rootful == "sudo",
		OutputDir:     outputDir,
		VmStateDir:    vmStateDir,
		Force:         req.Force,
		ExistingState: existingState,
	}

	switch vmSpec.Source.Kind {
	case "cloud_image":
		// Nothing further to resolve — BuildCloudImage fetches its own base image via
		// kit.FetchQcow2 (URL + checksum, sdk-importable) and needs no host-only lookup.

	case "bootc":
		if vmSpec.Source.Box == "" {
			return spec.VmBuildReply{}, fmt.Errorf("source.box is required for bootc VMs")
		}
		imageRef, rerr := resolveBootcImageRef(engine, vmSpec.Source.Box)
		if rerr != nil {
			return spec.VmBuildReply{}, rerr
		}
		reply.BootcImageRef = imageRef

	case "bootstrap":
		if err := resolveVmBuildBootstrap(ctx, ex, dir, engine, vmSpec, &reply); err != nil {
			return spec.VmBuildReply{}, err
		}

	default:
		return spec.VmBuildReply{}, fmt.Errorf("vm %q: unsupported source.kind %q (want one of %s)", boxName, vmSpec.Source.Kind, strings.Join(knownVmSourceKinds, ", "))
	}

	return reply, nil
}
