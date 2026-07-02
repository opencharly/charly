// Hand-written types for shapes `cue exp gengotypes` cannot express faithfully:
//
//   - PortSpec       — the candy `port:` element is a (int | "proto:port")
//     disjunction the Go normalizer canonicalizes to {port,
//     protocol}; gengotypes degrades the disjunction to `any`.
//   - VmDeployState  — MACHINE-WRITTEN runtime deploy state. Its CUE def carries
//     an open `...` tail (forward-evolving state record), which
//     makes gengotypes degrade the whole def to `map[string]any`.
//     The hand Go field is a CONCRETE struct, so #VmDeployState
//     is @go(-)'d and the faithful struct (+ its nested state
//     sub-types) is mirrored HERE.
//
// Ported from the package-main param structs (the source of truth); keep in
// lockstep until WF-B repoints package main onto this package. STANDALONE — no
// package-main symbol is referenced.
package spec

// PortSpec is the canonical {port, protocol} form of a candy `port:` entry.
// Source: charly/layers.go PortSpec (no struct tags — yaml.v3 lowercases the
// field name).
type PortSpec struct {
	Port     int
	Protocol string
}

// VmDeployState is the runtime state the vm lifecycle hook's PrepareVenue writes on first apply.
// Source: charly/deploy.go VmDeployState.
type VmDeployState struct {
	InstanceID              string                  `yaml:"instance_id,omitempty" json:"instance_id,omitempty"`
	DiskPath                string                  `yaml:"disk_path,omitempty" json:"disk_path,omitempty"`
	SeedIso                 string                  `yaml:"seed_iso,omitempty" json:"seed_iso,omitempty"`
	SshPort                 int                     `yaml:"ssh_port,omitempty" json:"ssh_port,omitempty"`
	SshUser                 string                  `yaml:"ssh_user,omitempty" json:"ssh_user,omitempty"`
	Backend                 string                  `yaml:"backend,omitempty" json:"backend,omitempty"`
	KeyInjectionResolved    *VmKeyInjectionResolved `yaml:"key_injection_resolved,omitempty" json:"key_injection_resolved,omitempty"`
	CharlyInstallStrategy   string                  `yaml:"charly_install_strategy,omitempty" json:"charly_install_strategy,omitempty"`
	CloudInitRenderedDigest string                  `yaml:"cloud_init_rendered_digest,omitempty" json:"cloud_init_rendered_digest,omitempty"`
	Snapshots               []VmSnapshotState       `yaml:"snapshot,omitempty" json:"snapshot,omitempty"`
	Ephemeral               *EphemeralRuntime       `yaml:"ephemeral,omitempty" json:"ephemeral,omitempty"`
}

// VmKeyInjectionResolved is the effective key-injection state. Source:
// charly/deploy.go VmKeyInjectionResolved.
type VmKeyInjectionResolved struct {
	SMBIOS    bool `yaml:"smbios" json:"smbios"`
	CloudInit bool `yaml:"cloud_init" json:"cloud_init"`
}

// VmSnapshotState mirrors one snapshot in the charly.yml vm_state record.
// Source: charly/deploy.go VmSnapshotState.
type VmSnapshotState struct {
	Name        string `yaml:"name" json:"name"`
	Mode        string `yaml:"mode" json:"mode"`
	LibvirtName string `yaml:"libvirt_name,omitempty" json:"libvirt_name,omitempty"`
	DiskPath    string `yaml:"disk_path,omitempty" json:"disk_path,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Created     string `yaml:"created,omitempty" json:"created,omitempty"`
	Parent      string `yaml:"parent,omitempty" json:"parent,omitempty"`
	Refcount    int    `yaml:"refcount" json:"refcount"`
}

// EphemeralRuntime is the live runtime state of an active ephemeral
// instantiation. Source: charly/deploy.go EphemeralRuntime.
type EphemeralRuntime struct {
	ID              string `yaml:"id" json:"id"`
	ParentVm        string `yaml:"parent_vm,omitempty" json:"parent_vm,omitempty"`
	ParentSnapshot  string `yaml:"parent_snapshot,omitempty" json:"parent_snapshot,omitempty"`
	ParentEphemeral string `yaml:"parent_ephemeral,omitempty" json:"parent_ephemeral,omitempty"`
	ChildRefcount   int    `yaml:"child_refcount,omitempty" json:"child_refcount,omitempty"`
	TimerUnit       string `yaml:"timer_unit,omitempty" json:"timer_unit,omitempty"`
	TtlDeadline     string `yaml:"ttl_deadline,omitempty" json:"ttl_deadline,omitempty"`
	Status          string `yaml:"status,omitempty" json:"status,omitempty"`
	InstanceName    string `yaml:"instance_name,omitempty" json:"instance_name,omitempty"`
}
