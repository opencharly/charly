package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// host_build_vm_build.go — the "vm-build" F10 host-builder: PREP+RESOLVE only (P8b-rest). The
// VM-disk build ENGINE (RunPrivileged pacstrap/bootc, BuildCloudImage/BuildBootcVM/BuildBootstrapVM,
// EmitDiskBuildScript) moved into candy/plugin-vm — the same inversion the box-build engine already
// went through behind HostBuild("build-prep") in P8b: the plugin owns the podman/qemu-img/bootc-install
// exec; the host resolves the kind:vm entity + the build vocabulary + the per-source-kind image refs
// into the spec.VmBuildReply envelope (LoadUnified, LoadBuildConfigForBox, resolveBootcImageRef,
// ensureBuilderImageBuilt — the loader + box-store Mechanisms a sdk-only candy cannot run) and returns.
const vmBuildBuilderKind = "vm-build"

// KnownVmSourceKinds lists the source.kind values supported by charly vm build. Used by error
// messages so adding a new kind keeps the user-facing enumeration in sync with the dispatch.
var KnownVmSourceKinds = []string{"cloud_image", "bootc", "bootstrap"}

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

// resolveVmBuildEntity loads the project + resolves boxName's kind:vm entity into a *VmSpec.
func resolveVmBuildEntity(dir, boxName string) (*VmSpec, error) {
	uf, ok, ufErr := LoadUnified(dir)
	if ufErr != nil || !ok || uf.VM == nil {
		return nil, noVmEntityErr(boxName)
	}
	body, hit := uf.VM[boxName]
	if !hit {
		return nil, noVmEntityErr(boxName)
	}
	return resolveVmViaPlugin(body)
}

// resolveVmBuildBootstrap resolves the distro/builder vocabulary + pre-builds the builder image
// for a "bootstrap" source.kind, filling reply.{BuilderImageRef,DistroJSON,BuilderJSON}.
func resolveVmBuildBootstrap(engine string, vmSpec *VmSpec, reply *spec.VmBuildReply) error {
	distroCfg, builderCfg, lerr := loadBuildYmlSections()
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
	builderRef, berr := ensureBuilderImageBuilt(engine, vmSpec.Source.BuilderImage)
	if berr != nil {
		return berr
	}
	distroJSON, jerr := marshalJSON(distro)
	if jerr != nil {
		return fmt.Errorf("marshalling resolved distro: %w", jerr)
	}
	builderJSON, jerr := marshalJSON(builder)
	if jerr != nil {
		return fmt.Errorf("marshalling resolved builder: %w", jerr)
	}
	reply.BuilderImageRef = builderRef
	reply.DistroJSON = distroJSON
	reply.BuilderJSON = builderJSON
	return nil
}

func hostBuildVmBuild(_ context.Context, req spec.VmBuildRequest, _ buildEngineContext) (spec.VmBuildReply, error) {
	dir, err := os.Getwd()
	if err != nil {
		return spec.VmBuildReply{}, err
	}

	boxName, imageTag := parseImageArg(req.Box)
	_ = imageTag // reserved for a future box-tag pin on the disk-build path; unused today, as before

	vmSpec, err := resolveVmBuildEntity(dir, boxName)
	if err != nil {
		return spec.VmBuildReply{}, err
	}

	rt, rtErr := ResolveRuntime()
	if rtErr != nil {
		return spec.VmBuildReply{}, rtErr
	}
	engine := "podman"
	if rt != nil {
		engine = EngineBinary(rt.RunEngine)
	}

	outputDir, err := filepath.Abs(vmDiskDir(boxName))
	if err != nil {
		return spec.VmBuildReply{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return spec.VmBuildReply{}, err
	}
	vmStateDir := filepath.Join(home, ".local", "share", "charly", "vm", "charly-"+boxName)
	if err := os.MkdirAll(vmStateDir, 0o755); err != nil {
		return spec.VmBuildReply{}, err
	}

	var existingState *spec.VmDeployState
	if e, ok := deploykit.LoadDeployConfigForRead("charly vm build").LookupKey("vm:" + boxName); ok {
		existingState = e.VmState
	}

	vmJSON, err := marshalJSON(vmSpec)
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
		if err := resolveVmBuildBootstrap(engine, vmSpec, &reply); err != nil {
			return spec.VmBuildReply{}, err
		}

	default:
		return spec.VmBuildReply{}, fmt.Errorf("vm %q: unsupported source.kind %q (want one of %s)", boxName, vmSpec.Source.Kind, strings.Join(KnownVmSourceKinds, ", "))
	}

	return reply, nil
}

// parseImageArg splits "image:tag" into (image, tag). If no colon, tag is empty.
func parseImageArg(arg string) (string, string) {
	if i := strings.LastIndex(arg, ":"); i > 0 {
		return arg[:i], arg[i+1:]
	}
	return arg, ""
}

// resolveBootcImageRef maps a bootc source.image to a concrete OCI ref. Stays HOST-SIDE
// (P8b-rest): it needs resolveLocalImageRef's cfg.Box + local podman-storage inspection, a
// core-only Mechanism a sdk-only candy cannot run itself.
//
// A full ref (containing "/", e.g. "quay.io/fedora/fedora-bootc:43" or a pinned
// "…@sha256:…") passes through unchanged — bootc may pull it from a registry. An internal
// kind:image short name (e.g. "fedora-bootc") resolves against local podman storage to its
// newest CalVer tag via the shared resolveLocalImageRef: charly is CalVer-only, so there is
// NO `:latest` fallback — the bootc image must be built first (`charly box build <name>`),
// which is surfaced as an actionable error when it is missing.
func resolveBootcImageRef(engine, image string) (string, error) {
	if strings.Contains(image, "/") {
		return image, nil
	}
	resolved, err := resolveLocalImageRef(engine, image)
	if err != nil {
		return "", fmt.Errorf("resolving bootc image %q: %w (build it first with `charly box build %s`)", image, err, image)
	}
	return resolved, nil
}

// loadBuildYmlSections loads the distro: + builder: blocks of the embedded build vocabulary
// (charly/charly.yml). Mirrors the loader path `charly box build` uses for the same data —
// bootstrap VM builds need the distro.<name>.pacstrap and .bootloader templates plus the
// matching builder.<name> bootstrap template.
func loadBuildYmlSections() (*DistroConfig, *BuilderConfig, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	dc, bc, _, err := LoadBuildConfigForBox(dir)
	return dc, bc, err
}

var _ = func() bool {
	registerHostBuilder(vmBuildBuilderKind, typedHostBuilder(vmBuildBuilderKind, hostBuildVmBuild))
	return true
}()
