package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// vm_import.go — VM adoption.
//
// Reads a libvirt domain's XML, maps it onto a VmSpec with
// source.kind: imported, and persists a kind:vm declaration to vm.yml.
//
// Imported VMs are tracked by charly for lifecycle (start/stop/ssh/console)
// and snapshot operations, but `charly vm build` is a no-op (the disk is
// externally managed). `charly vm clone --from <imported>@<snap>` works
// transparently because clone uses the snapshot's external file as
// backing.

// libvirtDomainXMLForImport is the subset of libvirt domain XML that
// import maps onto VmSpec fields.
type libvirtDomainXMLForImport struct {
	XMLName xml.Name                `xml:"domain"`
	Type    string                  `xml:"type,attr"`
	Name    string                  `xml:"name"`
	UUID    string                  `xml:"uuid"`
	Memory  libvirtMemoryForImport  `xml:"memory"`
	Vcpu    libvirtVcpuForImport    `xml:"vcpu"`
	OS      libvirtOSForImport      `xml:"os"`
	Devices libvirtDevicesForImport `xml:"devices"`
}

type libvirtMemoryForImport struct {
	Unit  string `xml:"unit,attr"`
	Value int64  `xml:",chardata"`
}

type libvirtVcpuForImport struct {
	Value int `xml:",chardata"`
}

type libvirtOSForImport struct {
	Type   libvirtOSTypeForImport    `xml:"type"`
	Loader *libvirtOSLoaderForImport `xml:"loader,omitempty"`
}

type libvirtOSTypeForImport struct {
	Arch    string `xml:"arch,attr"`
	Machine string `xml:"machine,attr"`
	Value   string `xml:",chardata"`
}

type libvirtOSLoaderForImport struct {
	Type     string `xml:"type,attr"`
	ReadOnly string `xml:"readonly,attr"`
	Secure   string `xml:"secure,attr"`
	Path     string `xml:",chardata"`
}

type libvirtDevicesForImport struct {
	Disks      []libvirtDiskForImport      `xml:"disk"`
	Interfaces []libvirtInterfaceForImport `xml:"interface"`
}

type libvirtDiskForImport struct {
	Type   string `xml:"type,attr"`
	Device string `xml:"device,attr"`
	Driver struct {
		Name string `xml:"name,attr"`
		Type string `xml:"type,attr"`
	} `xml:"driver"`
	Source struct {
		File string `xml:"file,attr"`
	} `xml:"source"`
	Target struct {
		Dev string `xml:"dev,attr"`
		Bus string `xml:"bus,attr"`
	} `xml:"target"`
}

type libvirtInterfaceForImport struct {
	Type string `xml:"type,attr"`
}

// ImportFromLibvirt reads the domain XML for `domainName`, maps it
// onto a VmSpec, and returns the (name, spec) pair ready to be written
// into vm.yml. The targetName argument lets the caller override the
// vm.yml entry key (default: domain name).
func ImportFromLibvirt(domainName, targetName string) (string, *VmSpec, error) {
	if domainName == "" {
		return "", nil, fmt.Errorf("import: domain name is required")
	}

	// Read the domain XML via the out-of-process vm plugin (go-libvirt moved there).
	raw, ok := invokeVmPlugin("domain-xml", domainName, libvirtSessionURI)
	if !ok {
		return "", nil, fmt.Errorf("vm plugin unavailable (go-libvirt is out-of-process)")
	}
	var dr struct {
		XML   string `json:"xml"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &dr); err != nil {
		return "", nil, err
	}
	if dr.Error != "" {
		return "", nil, fmt.Errorf("reading domain XML for %q: %s", domainName, dr.Error)
	}
	xmlStr := dr.XML

	var parsed libvirtDomainXMLForImport
	if err := xml.Unmarshal([]byte(xmlStr), &parsed); err != nil {
		return "", nil, fmt.Errorf("parsing domain XML: %w", err)
	}

	spec := &VmSpec{}
	mapMemoryToSpec(parsed.Memory, spec)
	mapVcpuToSpec(parsed.Vcpu, spec)
	mapOSToSpec(parsed.OS, spec)
	mapNetworkToSpec(parsed.Devices.Interfaces, spec)
	primaryDisk := mapDiskToSpec(parsed.Devices.Disks)

	now := time.Now().UTC().Format(time.RFC3339)
	spec.Source = VmSource{
		Kind:        "imported",
		LibvirtName: parsed.Name,
		DiskPath:    primaryDisk.Path,
		DiskFormat:  primaryDisk.Format,
		AdoptedAt:   now,
	}

	name := targetName
	if name == "" {
		// Strip any "charly-" prefix to avoid double-prefixing on subsequent
		// commands (which add their own charly- prefix).
		name = strings.TrimPrefix(parsed.Name, "charly-")
	}
	if name == "" {
		name = parsed.Name
	}
	return name, spec, nil
}

type primaryDiskInfo struct {
	Path   string
	Format string
	Bus    string
}

// mapDiskToSpec returns the path + format of the first <disk
// device='disk'> entry. Snapshots/cdroms are skipped. charly tracks one
// primary disk per VM in V1.
func mapDiskToSpec(disks []libvirtDiskForImport) primaryDiskInfo {
	for _, d := range disks {
		if d.Device != "disk" && d.Device != "" {
			continue
		}
		if d.Type != "file" && d.Type != "" {
			continue
		}
		return primaryDiskInfo{
			Path:   d.Source.File,
			Format: strings.ToLower(d.Driver.Type),
			Bus:    d.Target.Bus,
		}
	}
	return primaryDiskInfo{}
}

func mapMemoryToSpec(m libvirtMemoryForImport, spec *VmSpec) {
	// Convert to a human-readable size with the unit attribute (KiB,
	// MiB, GiB). Default unit is KiB if missing.
	unit := strings.ToLower(m.Unit)
	if unit == "" {
		unit = "kib"
	}
	bytesPerUnit := int64(1024) // KiB
	switch unit {
	case "kib", "kb":
		bytesPerUnit = 1024
	case "mib", "mb":
		bytesPerUnit = 1024 * 1024
	case "gib", "gb":
		bytesPerUnit = 1024 * 1024 * 1024
	case "b":
		bytesPerUnit = 1
	}
	totalBytes := m.Value * bytesPerUnit
	gib := totalBytes / (1024 * 1024 * 1024)
	if gib > 0 && totalBytes%(1024*1024*1024) == 0 {
		spec.Ram = fmt.Sprintf("%dG", gib)
		return
	}
	mib := totalBytes / (1024 * 1024)
	spec.Ram = fmt.Sprintf("%dM", mib)
}

func mapVcpuToSpec(v libvirtVcpuForImport, spec *VmSpec) {
	if v.Value > 0 {
		spec.Cpus = v.Value
	}
}

func mapOSToSpec(os libvirtOSForImport, spec *VmSpec) {
	if os.Type.Machine != "" {
		spec.Machine = os.Type.Machine
	}
	if os.Loader == nil {
		spec.Firmware = "bios"
		return
	}
	// UEFI: distinguish secure-boot-capable from insecure based on
	// loader path heuristics (libvirt records OVMF_CODE.fd vs
	// OVMF_CODE.secboot.fd).
	path := strings.ToLower(os.Loader.Path)
	switch {
	case strings.Contains(path, "secboot"):
		spec.Firmware = "uefi-secure"
	case os.Loader.Secure == "yes":
		spec.Firmware = "uefi-secure"
	default:
		spec.Firmware = "uefi-insecure"
	}
}

func mapNetworkToSpec(ifaces []libvirtInterfaceForImport, spec *VmSpec) {
	if len(ifaces) == 0 {
		return
	}
	t := strings.ToLower(ifaces[0].Type)
	switch t {
	case "user":
		spec.Network = &VmNetwork{Mode: "user"}
	case "bridge":
		spec.Network = &VmNetwork{Mode: "bridge"}
	case "network":
		spec.Network = &VmNetwork{Mode: "nat"}
	}
}

// ListUnmanagedDomains returns libvirt domains that are NOT recorded
// in charly's vm.yml (not yet imported).
func ListUnmanagedDomains() ([]string, error) {
	// List all domains via the out-of-process vm plugin (go-libvirt moved there).
	raw, ok := invokeVmPlugin("list-all-domains", "", libvirtSessionURI)
	if !ok {
		return nil, fmt.Errorf("vm plugin unavailable (go-libvirt is out-of-process)")
	}
	var lr struct {
		Names []string `json:"names"`
		Error string   `json:"error"`
	}
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, err
	}
	if lr.Error != "" {
		return nil, fmt.Errorf("listing domains: %s", lr.Error)
	}

	managed, _ := loadManagedVmSet()
	var out []string
	for _, name := range lr.Names {
		// Filter charly-managed domains (those with the charly- prefix that
		// also appear in vm.yml).
		charlyName := strings.TrimPrefix(name, "charly-")
		if managed[charlyName] || managed[name] {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// loadManagedVmSet returns the set of VM names recorded in the
// project's charly.yml. Returns an empty set on any error;
// ListUnmanagedDomains tolerates missing config gracefully.
//
// Schema v4 (2026-05): charly.yml is the canonical authoring root.
// A legacy per-kind vm.yml is reachable transparently via includes:,
// so a single read of charly.yml suffices.
func loadManagedVmSet() (map[string]bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return map[string]bool{}, err
	}
	uf, ok, err := LoadUnified(cwd)
	if err != nil || !ok || uf == nil {
		return map[string]bool{}, nil
	}
	out := map[string]bool{}
	for k := range uf.VM {
		out[k] = true
	}
	return out, nil
}

// WriteVmImportDeclaration persists a kind:vm declaration with
// source.kind: imported into charly.yml. Schema v4 (2026-05) makes
// charly.yml the only canonical authoring target; a legacy vm.yml
// at the project root is loaded transparently via includes: but new
// entries land in charly.yml.
func WriteVmImportDeclaration(name string, spec *VmSpec) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(cwd, UnifiedFileName)
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("charly.yml not found in %s; run `charly box new project .` first", cwd)
		}
		return fmt.Errorf("stat charly.yml: %w", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("reading %s: %w", target, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing %s: %w", target, err)
	}
	if root.Kind == 0 {
		root.Kind = yaml.DocumentNode
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top-level YAML is not a mapping", target)
	}
	topMap := root.Content[0]
	vmMap := findOrCreateMapEntry(topMap, "vm")
	if alreadyHas(vmMap, name) {
		return fmt.Errorf("%s: vm entry %q already exists", target, name)
	}
	entry := buildImportedVmNode(spec)
	vmMap.Content = append(vmMap.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		entry,
	)
	// Re-marshal with 4-space indent matching charly.yml's
	// canonical style.
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(&root); err != nil {
		return fmt.Errorf("marshaling YAML: %w", err)
	}
	_ = enc.Close()
	out := []byte(buf.String())
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	return os.Rename(tmp, target)
}

// UpdateImportedVm re-reads the libvirt XML for the named domain and
// overwrites only the source-derived fields on the matching vm.yml
// entry, preserving operator-authored sub-mappings (snapshots:,
// cloud_init:, ssh:, libvirt: by default — pass --replace-libvirt to
// overwrite). Returns the updated spec for caller-side reporting.
//
// Field-merge semantics (see /charly-internals:libvirt-renderer "VM adoption"):
//   - source.{libvirt_name,disk_path,disk_format} → replaced from XML
//   - source.adopted_at → preserved (first-import timestamp)
//   - source.last_synced_at → set to NOW
//   - ram, cpus, machine, firmware, network.mode → replaced from XML
//   - snapshots:, cloud_init:, ssh:, libvirt: → preserved by default
func UpdateImportedVm(name, domainName string, replaceLibvirt bool) (*VmSpec, error) {
	if name == "" {
		return nil, fmt.Errorf("update: vm.yml entry name is required")
	}
	if domainName == "" {
		domainName = name
	}

	// Read the fresh state from libvirt.
	_, freshSpec, err := ImportFromLibvirt(domainName, name)
	if err != nil {
		return nil, fmt.Errorf("re-reading libvirt XML for %q: %w", domainName, err)
	}
	freshSpec.Source.LastSyncedAt = time.Now().UTC().Format(time.RFC3339)

	// Locate the existing entry in charly.yml.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	target := filepath.Join(cwd, UnifiedFileName)
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("charly.yml not found in %s; run `charly box new project .`", cwd)
		}
		return nil, fmt.Errorf("stat charly.yml: %w", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", target, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", target, err)
	}
	if root.Kind == 0 || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: top-level YAML is not a mapping", target)
	}
	topMap := root.Content[0]
	vmMap := findOrCreateMapEntry(topMap, "vm")
	entryNode := findMapEntryByKey(vmMap, name)
	if entryNode == nil {
		return nil, fmt.Errorf("%s: no kind:vm entry %q to update; run `charly vm import %s` first", target, name, domainName)
	}

	// Preserve adopted_at from existing entry; preserve operator-
	// authored sub-mappings.
	preservedAdoptedAt := readSourceField(entryNode, "adopted_at")
	if preservedAdoptedAt != "" {
		freshSpec.Source.AdoptedAt = preservedAdoptedAt
	}

	// Overwrite source-derived fields field-by-field, preserving any
	// operator-added sibling keys at the top level (libvirt:, ssh:,
	// snapshots:, cloud_init:, etc.).
	replaceMapEntryByKey(entryNode, "source", buildImportedSourceNode(freshSpec))
	replaceScalarByKey(entryNode, "ram", freshSpec.Ram)
	replaceIntByKey(entryNode, "cpus", freshSpec.Cpus)
	replaceScalarByKey(entryNode, "machine", freshSpec.Machine)
	replaceScalarByKey(entryNode, "firmware", freshSpec.Firmware)
	if freshSpec.Network != nil {
		netNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		addStrPair(netNode, "mode", freshSpec.Network.Mode)
		replaceMapEntryByKey(entryNode, "network", netNode)
	}
	if replaceLibvirt {
		// V1 doesn't render a libvirt-block from imported VMs (the
		// renderer reads from libvirt directly), so when caller asks
		// to "replace", we drop the existing libvirt: key entirely.
		removeMapEntryByKey(entryNode, "libvirt")
	}

	// Re-marshal with 4-space indent matching charly.yml's style.
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("marshaling YAML: %w", err)
	}
	_ = enc.Close()
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(buf.String()), 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return nil, fmt.Errorf("renaming %s → %s: %w", tmp, target, err)
	}
	return freshSpec, nil
}

// DiffImported compares the live libvirt XML against the on-disk
// kind:vm entry's source-derived fields and returns a human-readable
// list of differences. Empty list means "no drift". Each line has the
// shape "field: <on-disk-value> → <fresh-value>".
//
// Schema v4 (2026-05): reads through LoadUnified so any vm.yml / vms.yml
// pulled in via includes: is transparent. The canonical authoring root
// is charly.yml.
func DiffImported(name, domainName string) ([]string, error) {
	if domainName == "" {
		domainName = name
	}
	_, freshSpec, err := ImportFromLibvirt(domainName, name)
	if err != nil {
		return nil, err
	}
	cwd, _ := os.Getwd()
	uf, ok, err := LoadUnified(cwd)
	if err != nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !ok || uf == nil {
		return nil, fmt.Errorf("charly.yml not found in %s", cwd)
	}
	existingPtr, _ := resolveVmViaPlugin(uf.VM[name])
	if existingPtr != nil {
		existing := *existingPtr
		var diffs []string
		if existing.Source.LibvirtName != freshSpec.Source.LibvirtName {
			diffs = append(diffs, fmt.Sprintf("source.libvirt_name: %q → %q", existing.Source.LibvirtName, freshSpec.Source.LibvirtName))
		}
		if existing.Source.DiskPath != freshSpec.Source.DiskPath {
			diffs = append(diffs, fmt.Sprintf("source.disk_path: %q → %q", existing.Source.DiskPath, freshSpec.Source.DiskPath))
		}
		if existing.Source.DiskFormat != freshSpec.Source.DiskFormat {
			diffs = append(diffs, fmt.Sprintf("source.disk_format: %q → %q", existing.Source.DiskFormat, freshSpec.Source.DiskFormat))
		}
		if existing.Ram != freshSpec.Ram {
			diffs = append(diffs, fmt.Sprintf("ram: %q → %q", existing.Ram, freshSpec.Ram))
		}
		if existing.Cpus != freshSpec.Cpus {
			diffs = append(diffs, fmt.Sprintf("cpus: %d → %d", existing.Cpus, freshSpec.Cpus))
		}
		if existing.Machine != freshSpec.Machine {
			diffs = append(diffs, fmt.Sprintf("machine: %q → %q", existing.Machine, freshSpec.Machine))
		}
		if existing.Firmware != freshSpec.Firmware {
			diffs = append(diffs, fmt.Sprintf("firmware: %q → %q", existing.Firmware, freshSpec.Firmware))
		}
		oldMode := ""
		if existing.Network != nil {
			oldMode = existing.Network.Mode
		}
		newMode := ""
		if freshSpec.Network != nil {
			newMode = freshSpec.Network.Mode
		}
		if oldMode != newMode {
			diffs = append(diffs, fmt.Sprintf("network.mode: %q → %q", oldMode, newMode))
		}
		return diffs, nil
	}
	return nil, fmt.Errorf("no kind:vm entry %q in charly.yml", name)
}

// findMapEntryByKey returns the value node of a key in a mapping,
// or nil if absent.
func findMapEntryByKey(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

// readSourceField reads a string value at entryNode.source.<key>.
func readSourceField(entryNode *yaml.Node, key string) string {
	src := findMapEntryByKey(entryNode, "source")
	if src == nil {
		return ""
	}
	v := findMapEntryByKey(src, key)
	if v == nil {
		return ""
	}
	return v.Value
}

// replaceScalarByKey replaces (or adds) a scalar string value at
// parent.<key>. Empty new-value removes the key.
func replaceScalarByKey(parent *yaml.Node, key, newVal string) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			if newVal == "" {
				parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
				return
			}
			parent.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: newVal}
			return
		}
	}
	if newVal == "" {
		return
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: newVal},
	)
}

// replaceIntByKey replaces (or adds) an int value at parent.<key>.
// Zero new-value removes the key.
func replaceIntByKey(parent *yaml.Node, key string, newVal int) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			if newVal == 0 {
				parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
				return
			}
			parent.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", newVal)}
			return
		}
	}
	if newVal == 0 {
		return
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", newVal)},
	)
}

// replaceMapEntryByKey replaces (or adds) a mapping value at
// parent.<key>.
func replaceMapEntryByKey(parent *yaml.Node, key string, newVal *yaml.Node) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = newVal
			return
		}
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		newVal,
	)
}

// removeMapEntryByKey removes the key/value pair at parent.<key>.
func removeMapEntryByKey(parent *yaml.Node, key string) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
			return
		}
	}
}

// buildImportedSourceNode builds the YAML mapping for the source:
// sub-block of an imported VM (used by UpdateImportedVm).
func buildImportedSourceNode(spec *VmSpec) *yaml.Node {
	source := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addStrPair(source, "kind", "imported")
	addStrPair(source, "libvirt_name", spec.Source.LibvirtName)
	if spec.Source.DiskPath != "" {
		addStrPair(source, "disk_path", spec.Source.DiskPath)
	}
	if spec.Source.DiskFormat != "" {
		addStrPair(source, "disk_format", spec.Source.DiskFormat)
	}
	if spec.Source.AdoptedAt != "" {
		addStrPair(source, "adopted_at", spec.Source.AdoptedAt)
	}
	if spec.Source.LastSyncedAt != "" {
		addStrPair(source, "last_synced_at", spec.Source.LastSyncedAt)
	}
	return source
}

// buildImportedVmNode constructs the YAML mapping for an imported VM
// spec.
func buildImportedVmNode(spec *VmSpec) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	source := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addStrPair(source, "kind", "imported")
	addStrPair(source, "libvirt_name", spec.Source.LibvirtName)
	if spec.Source.DiskPath != "" {
		addStrPair(source, "disk_path", spec.Source.DiskPath)
	}
	if spec.Source.DiskFormat != "" {
		addStrPair(source, "disk_format", spec.Source.DiskFormat)
	}
	if spec.Source.AdoptedAt != "" {
		addStrPair(source, "adopted_at", spec.Source.AdoptedAt)
	}
	n.Content = append(n.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "source"},
		source,
	)
	if spec.Ram != "" {
		addStrPair(n, "ram", spec.Ram)
	}
	if spec.Cpus > 0 {
		n.Content = append(n.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "cpus"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", spec.Cpus)},
		)
	}
	if spec.Machine != "" {
		addStrPair(n, "machine", spec.Machine)
	}
	if spec.Firmware != "" {
		addStrPair(n, "firmware", spec.Firmware)
	}
	if spec.Network != nil && spec.Network.Mode != "" {
		netNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		addStrPair(netNode, "mode", spec.Network.Mode)
		n.Content = append(n.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "network"},
			netNode,
		)
	}
	return n
}
