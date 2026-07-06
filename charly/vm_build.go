package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VmBuildCmd builds a QCOW2/RAW disk image from a bootc container image.
type VmBuildCmd struct {
	Box       string `arg:"" help:"Bootc image name"`
	Size      string `long:"size" help:"Override disk size (e.g. 20G, '20 GiB')"`
	RootSize  string `long:"root-size" help:"Override root partition size (e.g. 10G)"`
	Tag       string `long:"tag" help:"Image tag override"`
	Type      string `long:"type" default:"qcow2" help:"Output format: qcow2, raw"`
	Transport string `long:"transport" help:"Image transport: registry, containers-storage, oci, oci-archive"`
	Console   bool   `long:"console" help:"Enable console output for debugging"`
}

func (c *VmBuildCmd) Run() error {
	// Validate output type
	switch c.Type {
	case "qcow2", "raw":
	case "iso":
		return fmt.Errorf("iso format is not supported — use qcow2 or raw")
	default:
		return fmt.Errorf("unsupported disk type %q (valid: qcow2, raw)", c.Type)
	}

	// Parse image:tag format from positional arg
	boxName, imageTag := parseImageArg(c.Box)

	// charly is CalVer-only — if neither the positional arg nor --tag
	// specifies a version, resolve to the newest local CalVer by
	// short name; no `:latest` fallback.
	calverTag := ""
	if imageTag != "" {
		calverTag = imageTag
	} else if c.Tag != "" {
		calverTag = c.Tag
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	rt, rtErr := ResolveRuntime()
	if rtErr != nil {
		return rtErr
	}

	// --- New kind:vm entity path (D1, D4, D12) ---
	// When boxName matches a kind:vm entity in charly.yml, branch
	// into the VmSpec-driven build pipeline: cloud_image → fetch+
	// resize+seed ISO; bootc → bootc install reading the bootc-branch
	// fields from VmSpec.Source.
	if uf, ok, ufErr := LoadUnified(dir); ufErr == nil && ok && uf.VM != nil {
		if body, hit := uf.VM[boxName]; hit {
			spec, err := resolveVmViaPlugin(body)
			if err != nil {
				return err
			}
			return c.runVmSpecBuild(boxName, spec, rt)
		}
	}

	// Reached here = no `kind: vm` entity matched boxName. The legacy
	// BoxConfig.Vm / BoxConfig.Bootc fallback for VM builds was
	// removed in the VM hard-cutover. Users must declare a `kind: vm`
	// entity in vm.yml — paired with the bootc image if applicable.
	_ = calverTag
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

// parseImageArg splits "image:tag" into (image, tag). If no colon, tag is empty.
func parseImageArg(arg string) (string, string) {
	if i := strings.LastIndex(arg, ":"); i > 0 {
		return arg[:i], arg[i+1:]
	}
	return arg, ""
}

// KnownVmSourceKinds lists the source.kind values supported by charly vm build.
// Used by error messages so adding a new kind keeps the user-facing
// enumeration in sync with the dispatch.
var KnownVmSourceKinds = []string{"cloud_image", "bootc", "bootstrap"}

// runVmSpecBuild handles `charly vm build <vm-name>` where <vm-name>
// matches a kind:vm entity. Dispatches on source.kind to the appropriate
// per-kind builder (BuildCloudImage / BuildBootcVM / BuildBootstrapVM).
func (c *VmBuildCmd) runVmSpecBuild(vmName string, spec *VmSpec, rt *ResolvedRuntime) error {
	fmt.Fprintf(os.Stderr, "Building VM %q (source.kind=%s)\n", vmName, spec.Source.Kind)

	outputDir, err := filepath.Abs(vmDiskDir(vmName))
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	vmStateDir := filepath.Join(home, ".local", "share", "charly", "vm", "charly-"+vmName)
	if err := os.MkdirAll(vmStateDir, 0o755); err != nil {
		return err
	}
	var existingState *VmDeployState
	if e, ok := loadDeployConfigForRead("charly vm build").LookupKey("vm:" + vmName); ok {
		existingState = e.VmState
	}

	switch spec.Source.Kind {
	case "cloud_image":
		res, err := BuildCloudImage(spec, outputDir, vmStateDir, existingState)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Wrote %s (base sha256=%s)\n", res.DiskPath, res.BaseImageSHA256)
		fmt.Fprintf(os.Stderr, "Wrote %s\n", res.SeedIsoPath)
		fmt.Fprintf(os.Stderr, "Instance-id: %s\n", res.InstanceID)
		return nil

	case "bootc":
		res, err := BuildBootcVM(spec, outputDir, vmStateDir, existingState, rt.BuildEngine)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Wrote %s\n", res.DiskPath)
		if res.SeedIsoPath != "" {
			fmt.Fprintf(os.Stderr, "Wrote %s\n", res.SeedIsoPath)
		}
		return nil

	case "bootstrap":
		// Resolve the builder + distro build vocabulary (from the project's
		// charly.yml import: plus the binary-embedded default; handled internally
		// by the runtime — here we look them up from the runtime).
		distroCfg, builderCfg, err := loadBuildYmlSections()
		if err != nil {
			return fmt.Errorf("loading builder/distro sections from the embedded build vocabulary: %w", err)
		}
		res, err := BuildBootstrapVM(spec, outputDir, vmStateDir, existingState, distroCfg, builderCfg)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Wrote %s (rootfs sha256=%s)\n", res.DiskPath, res.BaseImageSHA256)
		if res.SeedIsoPath != "" {
			fmt.Fprintf(os.Stderr, "Wrote %s\n", res.SeedIsoPath)
		}
		return nil

	default:
		return fmt.Errorf("vm %q: unsupported source.kind %q (want one of %s)", vmName, spec.Source.Kind, strings.Join(KnownVmSourceKinds, ", "))
	}
}

// loadBuildYmlSections loads the distro: + builder: blocks of the embedded
// build vocabulary (charly/charly.yml). Mirrors the loader path used by charly
// box build for the same
// data — bootstrap VM builds need the distro.<name>.pacstrap and
// .bootloader templates plus the matching builder.<name> bootstrap
// template.
func loadBuildYmlSections() (*DistroConfig, *BuilderConfig, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	dc, bc, _, err := LoadBuildConfigForBox(dir)
	return dc, bc, err
}
