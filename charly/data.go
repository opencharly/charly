package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/kit"
)

// DataProvisionMode controls how provisionData copies files.
type DataProvisionMode string

const (
	// DataProvisionInitial copies all data into empty directories (cp -a).
	// Used by charly config for first-time setup.
	DataProvisionInitial DataProvisionMode = "initial"

	// DataProvisionMerge adds missing files without overwriting existing ones (cp -an).
	// Used by charly update to add new data while preserving user modifications.
	DataProvisionMerge DataProvisionMode = "merge"

	// DataProvisionForce overwrites all data unconditionally (cp -a).
	// Used by charly config --force-seed.
	DataProvisionForce DataProvisionMode = "force"
)

// SeederHelperImage is a small runnable image used to copy data from a
// FROM-scratch data image into a named volume. Pulled lazily on first use.
// busybox:stable has cp + sh and is ~2MB.
const SeederHelperImage = "docker.io/library/busybox:stable"

// seedKind distinguishes how a data-target volume is backed at the podman
// level, because the seeding command differs in two ways: the -v source
// (host path vs volume name) and whether --userns=keep-id applies.
type seedKind int

const (
	// seedKindBind — target is a host directory. The runtime container runs
	// with UserNS=keep-id to align host UID with in-container UID, so the
	// seeder must match.
	seedKindBind seedKind = iota
	// seedKindNamed — target is a podman named volume. The runtime container
	// runs without keep-id (named volumes live in the rootless subuid space),
	// so the seeder must NOT apply keep-id or the runtime won't be able to
	// read the freshly-seeded files.
	seedKindNamed
)

func (k seedKind) label() string {
	switch k {
	case seedKindBind:
		return "bind"
	case seedKindNamed:
		return "named"
	}
	return "unknown"
}

// seedTarget is the common shape used by the seeding dispatch regardless of
// volume kind.
type seedTarget struct {
	bareName string // bare volume name (no "charly-<image>-" prefix)
	kind     seedKind
	// mountSource is passed to -v <src>:/seed. For bind: absolute host path.
	// For named: the full volume name (e.g. "charly-jupyter-workspace"). Podman
	// auto-discriminates based on whether the value starts with '/'.
	mountSource string
}

// Command execution seams. Tests replace these with recording stubs.
//
// dataCmdRun executes a command with stdout/stderr inherited from the parent.
// dataCmdOutput captures stdout and returns it alongside any error.
var (
	dataCmdRun = func(name string, args ...string) error {
		c := exec.Command(name, args...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	dataCmdOutput = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	}
)

// provisionData copies data from image staging (/data/) into the runtime
// volumes — both bind-mounted host directories AND podman named volumes.
// It matches data entries from image metadata against the resolved volume
// list by bare volume name.
//
// Volume kind determines the seeding command: bind mounts use the host path
// as the -v source with --userns=keep-id, named volumes use the full volume
// name as the -v source WITHOUT keep-id (the rootless subuid mapping handles
// ownership for named volumes).
//
// For data images (FROM scratch, no shell), named-volume targets use podman's
// --mount type=image to expose the staging filesystem and a lightweight helper
// image (busybox) to run cp. Bind-mount targets use the simpler podman
// create + podman cp path.
func provisionData(engine string, imageRef string, meta *BoxMetadata,
	bindMounts []ResolvedBindMount, namedVolumes []VolumeMount,
	deployName, instance string, mode DataProvisionMode) (int, error) {

	if len(meta.DataEntries) == 0 {
		return 0, nil
	}
	if len(bindMounts) == 0 && len(namedVolumes) == 0 {
		return 0, nil
	}

	// Build a unified targets map keyed by bare volume name. A volume
	// declared as bind wins over a named entry with the same bare name
	// (this should not happen in practice since ResolveVolumeBacking routes
	// each labelVolume to exactly one of the two slices). Instance-aware:
	// for instance deploys the volume name carries an extra <instance>
	// segment that must be stripped along with the image prefix —
	// BareVolumeName does both. Pre-fix this site used a bare TrimPrefix
	// that only stripped the image segment, so instance-rewritten volumes
	// (e.g. charly-versa-ecovoyage-workspace) reduced to `ecovoyage-workspace`
	// and never matched data entries whose `volume:` field is `workspace`,
	// causing the seeder to silently skip every data entry on every
	// instance deploy.
	targets := make(map[string]seedTarget, len(bindMounts)+len(namedVolumes))
	for _, nv := range namedVolumes {
		bare := kit.BareVolumeName(nv.VolumeName, deployName, instance)
		targets[bare] = seedTarget{
			bareName:    bare,
			kind:        seedKindNamed,
			mountSource: nv.VolumeName,
		}
	}
	for _, bm := range bindMounts {
		targets[bm.Name] = seedTarget{
			bareName:    bm.Name,
			kind:        seedKindBind,
			mountSource: bm.HostPath,
		}
	}

	seeded := 0
	for _, entry := range meta.DataEntries {
		target, ok := targets[entry.Volume]
		if !ok {
			// Surface the typo rather than silently dropping the entry.
			// This catches cases where a candy declares data: volume: <name>
			// but the bare name doesn't match any volume in the composed
			// image. charly validate also catches this at build time, but this
			// is the runtime safety net.
			fmt.Fprintf(os.Stderr,
				"  data entry references unknown volume %q (candy=%s, staging=%s) — skipping\n",
				entry.Volume, entry.Candy, entry.Staging)
			continue
		}

		// Human-readable label for diagnostics (volume + dest + kind)
		displayName := entry.Volume
		if entry.Dest != "" {
			displayName = entry.Volume + "/" + entry.Dest
		}
		displayName = displayName + " (" + target.kind.label() + ")"

		// Gate on mode.
		//
		// For bind targets we check the per-entry subdirectory so that
		// multiple entries with distinct Dest values each get their own
		// emptiness check. For named targets we check the volume root —
		// there's no cheap host-side view of sub-paths inside a named
		// volume without exec'ing into a container, and the practical
		// semantic ("treat the whole volume as empty on first seed") is
		// sufficient for the primary use case.
		switch mode {
		case DataProvisionInitial:
			var empty bool
			switch target.kind {
			case seedKindBind:
				checkPath := target.mountSource
				if entry.Dest != "" {
					checkPath = checkPath + "/" + entry.Dest
				}
				empty = isDirEmpty(checkPath)
			case seedKindNamed:
				empty = volumeIsEmpty(engine, target.mountSource)
			}
			if !empty {
				fmt.Fprintf(os.Stderr, "  %s: skipping (not empty)\n", displayName)
				continue
			}
		case DataProvisionMerge:
			// Always attempt merge (cp -an is safe on non-empty dirs)
		case DataProvisionForce:
			// Always copy (overwrites existing)
		}

		fmt.Fprintf(os.Stderr, "  %s: provisioning from %s ...\n", displayName, entry.Staging)

		// For bind targets, ensure the host directory exists. Named
		// volumes are created implicitly by podman on first -v use.
		if target.kind == seedKindBind {
			hostPath := target.mountSource
			if entry.Dest != "" {
				hostPath = hostPath + "/" + entry.Dest
			}
			if err := os.MkdirAll(hostPath, 0755); err != nil {
				return seeded, fmt.Errorf("creating directory %s: %w", hostPath, err)
			}
		}

		var err error
		if meta.DataImage {
			err = provisionFromScratchImage(engine, imageRef, entry, target, mode)
		} else {
			err = provisionFromRunnableImage(engine, imageRef, meta, entry, target, mode)
		}
		if err != nil {
			return seeded, fmt.Errorf("provisioning %s: %w", displayName, err)
		}

		fmt.Fprintf(os.Stderr, "  %s: done\n", displayName)
		seeded++
	}

	return seeded, nil
}

// provisionFromRunnableImage copies data from a regular (runnable) image
// by running a temporary container with cp. Dispatches on target.kind to
// get the correct -v source and UserNS handling.
func provisionFromRunnableImage(engine string, imageRef string, meta *BoxMetadata,
	entry LabelDataEntry, target seedTarget, mode DataProvisionMode) error {

	binary := EngineBinary(engine)

	cpFlag := "cp -a"
	if mode == DataProvisionMerge {
		cpFlag = "cp -an" // no-clobber: add new files, preserve existing
	}

	// In-container destination. entry.Dest subdirs are made explicit via
	// mkdir so the cp always targets an existing directory.
	contDest := "/seed"
	if entry.Dest != "" {
		contDest = "/seed/" + entry.Dest
	}

	args := []string{
		binary, "run", "--rm",
		"-v", target.mountSource + ":/seed",
	}
	if target.kind == seedKindBind && engine == "podman" {
		// Host path is owned by the real user UID; align the container's
		// in-user-namespace UID to match so cp produces files the host
		// user (and the runtime container with the same keep-id) can read.
		args = append(args,
			fmt.Sprintf("--userns=keep-id:uid=%d,gid=%d", meta.UID, meta.GID))
	}
	// Named volumes intentionally run without --userns=keep-id: the named
	// volume lives in the rootless subuid space, the runtime container
	// reads it without keep-id, so the seeder must write with the same
	// identity.

	args = append(args, imageRef, "bash", "-c",
		fmt.Sprintf("mkdir -p %q && %s %s. %q/ 2>&1", contDest, cpFlag, entry.Staging, contDest))

	return dataCmdRun(args[0], args[1:]...)
}

// provisionFromScratchImage copies data from a scratch-based data image.
// Bind-mount targets use the simple podman create + podman cp path. Named-
// volume targets use podman run with --mount type=image to expose the
// scratch image's filesystem and a lightweight helper image (busybox) as
// the runnable side, since scratch images have no shell and podman cp
// cannot target a named volume directly.
func provisionFromScratchImage(engine string, imageRef string,
	entry LabelDataEntry, target seedTarget, mode DataProvisionMode) error {

	if target.kind == seedKindBind {
		hostPath := target.mountSource
		if entry.Dest != "" {
			hostPath = hostPath + "/" + entry.Dest
		}
		return provisionFromScratchImageToHost(engine, imageRef, entry, hostPath)
	}

	// Named-volume path.
	if err := ensureSeederHelperImage(engine); err != nil {
		return fmt.Errorf("pulling seeder helper image: %w", err)
	}

	binary := EngineBinary(engine)

	cpFlag := "cp -a"
	if mode == DataProvisionMerge {
		cpFlag = "cp -an"
	}

	contDest := "/seed"
	if entry.Dest != "" {
		contDest = "/seed/" + entry.Dest
	}

	args := []string{
		binary, "run", "--rm",
		"-v", target.mountSource + ":/seed",
		"--mount", "type=image,src=" + imageRef + ",dst=/staging,rw=false",
		SeederHelperImage, "sh", "-c",
		fmt.Sprintf("mkdir -p %q && %s /staging%s. %q/ 2>&1", contDest, cpFlag, entry.Staging, contDest),
	}

	return dataCmdRun(args[0], args[1:]...)
}

// provisionFromScratchImageToHost is the original podman create + podman cp
// path, preserved for bind-mount targets where podman cp can write directly
// to the host filesystem. Named-volume targets cannot use this path because
// podman cp requires a host-visible destination.
func provisionFromScratchImageToHost(engine string, imageRef string,
	entry LabelDataEntry, hostPath string) error {
	binary := EngineBinary(engine)
	containerName := fmt.Sprintf("charly-data-seed-%d", os.Getpid())

	// Create a throwaway container from the scratch image. It doesn't need
	// to run — we only need a filesystem view to cp from.
	if err := dataCmdRun(binary, "create", "--name", containerName, imageRef); err != nil {
		return fmt.Errorf("creating container: %w", err)
	}
	defer func() { _ = dataCmdRun(binary, "rm", containerName) }()

	src := fmt.Sprintf("%s:%s.", containerName, entry.Staging)
	return dataCmdRun(binary, "cp", src, hostPath+"/")
}

// ensureSeederHelperImage lazily pulls the seeder helper image if it's not
// already present locally. The helper is used only for seeding a named
// volume from a FROM-scratch data image; every other seeding path uses the
// target image's own filesystem.
func ensureSeederHelperImage(engine string) error {
	binary := EngineBinary(engine)
	// Cheap "already present?" check — image exists returns 0 if the
	// image is in local storage.
	if err := dataCmdRun(binary, "image", "exists", SeederHelperImage); err == nil {
		return nil
	}
	return dataCmdRun(binary, "pull", SeederHelperImage)
}

// isDirEmpty returns true if the directory is empty, doesn't exist, or is not a directory.
func isDirEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return true // treat errors (including not-exist) as empty
	}
	return len(entries) == 0
}

// volumeIsEmpty returns true if the named podman volume does not exist, its
// mountpoint is unreadable, or the mountpoint directory contains no entries.
// Consistent with isDirEmpty's "treat errors as empty" semantics — used only
// by DataProvisionInitial gating, where the safe behavior on ambiguity is
// to attempt the seed (the cp itself will report any failure).
func volumeIsEmpty(engine, volumeName string) bool {
	binary := EngineBinary(engine)
	out, err := dataCmdOutput(binary, "volume", "inspect",
		"--format", "{{.Mountpoint}}", volumeName)
	if err != nil {
		return true // volume doesn't exist yet
	}
	mp := strings.TrimSpace(string(out))
	if mp == "" {
		return true
	}
	return isDirEmpty(mp)
}
