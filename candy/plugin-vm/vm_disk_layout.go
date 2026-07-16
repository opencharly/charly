package vm

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/opencharly/sdk/buildkit"
)

// vm_disk_layout.go — the partitioned-disk script emitter (P8b-rest: ported verbatim from
// charly/vm_disk_builder.go; pure text/template rendering, zero core dependency).

// DiskLayout describes a partitioned VM disk to be built inside a privileged container by
// EmitDiskBuildScript. Used by both the pacstrap/debootstrap VM-bootstrap path and the
// bootc-VM install path (where the bootloader is provided by `bootc install` rather than by
// chroot grub-install).
type DiskLayout struct {
	// SizeBytesOrSuffix is the size to allocate for the raw disk file (e.g. "20G", "10240M",
	// "536870912"). Forwarded verbatim to `truncate -s`.
	SizeBytesOrSuffix string
	// Rootfs selects the root partition filesystem: "ext4" (default), "xfs", or "btrfs".
	Rootfs string
	// EspSizeMib sizes the EFI System Partition (FAT32). Default 512.
	EspSizeMib int
	// Mnt is the absolute path inside the container where the root partition gets mounted
	// (default /mnt). Bootloader install templates render against this.
	Mnt string
}

// diskBuildScriptTmpl renders the partition + format + mount sequence for a fresh VM disk. The
// caller appends the rootfs install + chroot commands after `# >>> install rootfs <<<` and
// before the finalization (`# <<< install rootfs >>>`) markers.
//
// Layout (matches the standard Debian/Ubuntu installer layout):
//
//	/out/disk.raw         — sparse raw disk
//	/dev/loopX{p1,p2}     — ESP, root partitions (X varies)
//	{{.Mnt}}              — root partition mounted (ext4/xfs/btrfs).
//	                        /boot lives here too, so kernel images,
//	                        initramfs files, and their compatibility
//	                        symlinks (e.g. /boot/vmlinuz on Ubuntu)
//	                        land on a Unix-permission-aware filesystem.
//	{{.Mnt}}/boot/efi     — ESP mounted (FAT32). Only EFI binaries
//	                        (BOOTX64.EFI, grubx64.efi) live here.
//
// The script unmounts and detaches the loop device on exit (trap), then `qemu-img convert`
// produces /out/disk.qcow2.
const diskBuildScriptTmpl = `set -euo pipefail
mkdir -p /out
RAW=/out/disk.raw
truncate -s {{.SizeBytesOrSuffix}} "$RAW"
LOOP=$(losetup --find --show --partscan "$RAW")
trap '
  umount {{.Mnt}}/boot/efi 2>/dev/null || true
  umount {{.Mnt}} 2>/dev/null || true
  losetup -d "$LOOP" 2>/dev/null || true
' EXIT
parted -s "$LOOP" \
  mklabel gpt \
  mkpart ESP fat32 1MiB {{.EspSizeMib}}MiB \
  set 1 esp on \
  mkpart root {{.Rootfs}} {{.EspSizeMib}}MiB 100%
partprobe "$LOOP" || true
mkfs.fat -F32 "${LOOP}p1"
{{.MkfsCmd}} "${LOOP}p2"
mkdir -p {{.Mnt}}
mount "${LOOP}p2" {{.Mnt}}
mkdir -p {{.Mnt}}/boot/efi
mount "${LOOP}p1" {{.Mnt}}/boot/efi
# >>> install rootfs <<<
`

// diskBuildFinalizeTmpl renders the unmount + qcow2 conversion tail. Combined with
// diskBuildScriptTmpl + caller-supplied install body to form the full bash body passed to
// RunPrivileged.
const diskBuildFinalizeTmpl = `# <<< install rootfs >>>
sync
umount {{.Mnt}}/boot/efi
umount {{.Mnt}}
losetup -d "$LOOP"
trap - EXIT
qemu-img convert -O qcow2 "$RAW" /out/disk.qcow2
rm -f "$RAW"
`

// EmitDiskBuildScript renders the prelude (partition + format + mount) and finalize (unmount +
// qcow2 convert) halves of a privileged VM disk-build script. The caller stitches them around
// its own rootfs install body. Returns (prelude, finalize) on success.
func EmitDiskBuildScript(layout DiskLayout) (string, string, error) {
	if layout.Rootfs == "" {
		layout.Rootfs = "ext4"
	}
	if layout.EspSizeMib == 0 {
		layout.EspSizeMib = 512
	}
	if layout.Mnt == "" {
		layout.Mnt = "/mnt"
	}
	mkfs := ""
	switch layout.Rootfs {
	case "ext4":
		mkfs = "mkfs.ext4 -F"
	case "xfs":
		mkfs = "mkfs.xfs -f"
	case "btrfs":
		mkfs = "mkfs.btrfs -f"
	default:
		return "", "", fmt.Errorf("unsupported rootfs %q (want ext4|xfs|btrfs)", layout.Rootfs)
	}
	ctx := struct {
		SizeBytesOrSuffix string
		Rootfs            string
		EspSizeMib        int
		Mnt               string
		MkfsCmd           string
	}{
		SizeBytesOrSuffix: layout.SizeBytesOrSuffix,
		Rootfs:            layout.Rootfs,
		EspSizeMib:        layout.EspSizeMib,
		Mnt:               layout.Mnt,
		MkfsCmd:           mkfs,
	}
	prelude, err := renderTmpl("disk-prelude", diskBuildScriptTmpl, ctx)
	if err != nil {
		return "", "", err
	}
	finalize, err := renderTmpl("disk-finalize", diskBuildFinalizeTmpl, ctx)
	if err != nil {
		return "", "", err
	}
	return prelude, finalize, nil
}

func renderTmpl(name, tmpl string, ctx any) (string, error) {
	t, err := template.New(name).Funcs(buildkit.TemplateFuncs).Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return buf.String(), nil
}
