package main

import (
	"encoding/xml"
	"fmt"
	"os"

	libvirt "github.com/digitalocean/go-libvirt"
)

// vm_snapshot_libvirt.go — external qcow2 snapshot operations via
// libvirt. External snapshots create a new qcow2 file with the existing
// disk as backing chain; the running domain pivots to the new file as
// its active disk. Clone-from-snapshot uses the (frozen) backing file
// as the source for fresh COW overlays.

// snapshotXMLDesc is the libvirt domain-snapshot XML element. Composed
// in createExternalSnapshot to drive DomainSnapshotCreateXML.
type snapshotXMLDesc struct {
	XMLName     xml.Name           `xml:"domainsnapshot"`
	Name        string             `xml:"name"`
	Description string             `xml:"description,omitempty"`
	Disks       *snapshotDisksList `xml:"disks,omitempty"`
}

type snapshotDisksList struct {
	Disks []snapshotDisk `xml:"disk"`
}

type snapshotDisk struct {
	Name     string            `xml:"name,attr"`
	Snapshot string            `xml:"snapshot,attr"`
	Source   *snapshotDiskSrc  `xml:"source,omitempty"`
	Driver   *snapshotDiskDrvr `xml:"driver,omitempty"`
}

type snapshotDiskSrc struct {
	File string `xml:"file,attr"`
}

type snapshotDiskDrvr struct {
	Type string `xml:"type,attr"`
}

// createExternalSnapshot drives `virsh snapshot-create-as --disk-only`
// equivalent via libvirt. Produces an overlay qcow2 (`outFile`) whose
// backing file is the VM's current disk; the running VM pivots to the
// overlay as its active disk and the previous disk becomes immutable.
//
// outFile is the absolute path the snapshot's overlay qcow2 should be
// written to (passed in by the caller; vm_snapshot.go's
// snapshotExternalDiskPath constructs it).
//
// The overlay qcow2 referenced by libvirt becomes the live disk. The
// "frozen" disk (the one that existed before the snapshot) is what
// clones use as backing.
func createExternalSnapshot(opts SnapshotCreateOpts, outFile string) error {
	// Resolve current libvirt connection.
	uri := readVmBackendURI()
	conn, err := connectLibvirt(uri)
	if err != nil {
		return fmt.Errorf("connecting to libvirt: %w", err)
	}
	defer conn.Close() //nolint:errcheck

	domName := "charly-" + opts.VmName
	dom, err := conn.lookupDomain(domName)
	if err != nil {
		return fmt.Errorf("looking up domain %q: %w", domName, err)
	}

	// Identify the disk's libvirt target name (e.g. "vda"). Read the
	// running domain XML and extract the first <disk type='file'>'s
	// <target dev='...'/> attribute.
	xmlStr, err := conn.l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return fmt.Errorf("reading domain XML: %w", err)
	}
	targetDev, err := firstDiskTargetDev(xmlStr)
	if err != nil {
		return err
	}

	desc := snapshotXMLDesc{
		Name:        opts.SnapName,
		Description: opts.Description,
		Disks: &snapshotDisksList{
			Disks: []snapshotDisk{
				{
					Name:     targetDev,
					Snapshot: "external",
					Source:   &snapshotDiskSrc{File: outFile},
					Driver:   &snapshotDiskDrvr{Type: "qcow2"},
				},
			},
		},
	}
	xmlBytes, err := xml.MarshalIndent(desc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling snapshot XML: %w", err)
	}

	flags := libvirt.DomainSnapshotCreateDiskOnly
	if opts.Quiesce {
		flags |= libvirt.DomainSnapshotCreateQuiesce
	}
	flags |= libvirt.DomainSnapshotCreateAtomic
	if _, err := conn.l.DomainSnapshotCreateXML(dom, string(xmlBytes), uint32(flags)); err != nil {
		// On guests without qemu-guest-agent, the Quiesce flag may
		// fail. Retry without it as a fallback when --quiesce was
		// requested but no agent is available.
		if opts.Quiesce {
			fmt.Fprintln(os.Stderr, "note: quiesce failed (qemu-guest-agent not available?); retrying without --quiesce")
			flags &^= libvirt.DomainSnapshotCreateQuiesce
			if _, err2 := conn.l.DomainSnapshotCreateXML(dom, string(xmlBytes), uint32(flags)); err2 != nil {
				return fmt.Errorf("DomainSnapshotCreateXML: %w (after quiesce-fallback)", err2)
			}
			return nil
		}
		return fmt.Errorf("DomainSnapshotCreateXML: %w", err)
	}
	return nil
}

// deleteExternalSnapshot removes a libvirt snapshot record. Note: this
// does NOT automatically delete the backing-chain qcow2 files; the
// caller (vm_snapshot.go::DeleteSnapshot) removes the per-snapshot
// directory after the libvirt-side delete completes.
func deleteExternalSnapshot(vmName string, entry *SnapshotEntry) error {
	uri := readVmBackendURI()
	conn, err := connectLibvirt(uri)
	if err != nil {
		// libvirt may not have a record for this snapshot if it was
		// created earlier and the daemon was restarted (libvirt does
		// persist snapshot metadata, but be tolerant). Log and
		// continue to filesystem-level cleanup.
		fmt.Fprintf(os.Stderr, "note: connecting to libvirt for snapshot delete: %v (continuing with FS cleanup)\n", err)
		return nil
	}
	defer conn.Close() //nolint:errcheck

	domName := "charly-" + vmName
	dom, err := conn.lookupDomain(domName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: looking up domain %q for snapshot delete: %v (continuing with FS cleanup)\n", domName, err)
		return nil
	}

	libvirtName := entry.LibvirtName
	if libvirtName == "" {
		libvirtName = entry.Name
	}
	snap, err := conn.l.DomainSnapshotLookupByName(dom, libvirtName, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: snapshot %q not found via libvirt (already deleted?): %v\n", libvirtName, err)
		return nil
	}
	if err := conn.l.DomainSnapshotDelete(snap, 0); err != nil {
		return fmt.Errorf("DomainSnapshotDelete %q: %w", libvirtName, err)
	}
	return nil
}

// revertExternalSnapshot drives `virsh snapshot-revert` against the
// named snapshot. The running domain rebases to the snapshot's
// recorded state.
func revertExternalSnapshot(vmName string, entry *SnapshotEntry) error {
	uri := readVmBackendURI()
	conn, err := connectLibvirt(uri)
	if err != nil {
		return fmt.Errorf("connecting to libvirt: %w", err)
	}
	defer conn.Close() //nolint:errcheck

	domName := "charly-" + vmName
	dom, err := conn.lookupDomain(domName)
	if err != nil {
		return fmt.Errorf("looking up domain %q: %w", domName, err)
	}
	libvirtName := entry.LibvirtName
	if libvirtName == "" {
		libvirtName = entry.Name
	}
	snap, err := conn.l.DomainSnapshotLookupByName(dom, libvirtName, 0)
	if err != nil {
		return fmt.Errorf("DomainSnapshotLookupByName %q: %w", libvirtName, err)
	}
	if err := conn.l.DomainRevertToSnapshot(snap, 0); err != nil {
		return fmt.Errorf("DomainRevertToSnapshot %q: %w", libvirtName, err)
	}
	return nil
}

// firstDiskTargetDev parses domain XML and returns the first
// <disk><target dev='...'/></disk> attribute.
func firstDiskTargetDev(domainXML string) (string, error) {
	var dom struct {
		Devices struct {
			Disks []struct {
				Target struct {
					Dev string `xml:"dev,attr"`
				} `xml:"target"`
			} `xml:"disk"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(domainXML), &dom); err != nil {
		return "", fmt.Errorf("parsing domain XML: %w", err)
	}
	for _, d := range dom.Devices.Disks {
		if d.Target.Dev != "" {
			return d.Target.Dev, nil
		}
	}
	return "", fmt.Errorf("no <disk><target dev=/></disk> in domain XML")
}

// readVmBackendURI returns the libvirt URI to use for snapshot operations.
// V1 always uses the local session URI (matches the rest of the VM
// command surface; remote operation is via SSH-tunneled libvirt URI
// which is set by the caller's environment).
func readVmBackendURI() string {
	return libvirtSessionURI
}
