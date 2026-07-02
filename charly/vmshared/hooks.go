package vmshared

// hooks.go — the injection seams the shared VM / cloud-init orchestration needs
// from its consumer. Each of these symbols has a DIFFERENT implementation in the
// two programs that build this package into themselves:
//
//   - the charly core (charly/, package main) wires the host-side versions:
//     the real CUE egress validator, the embedded charly.yml decoder, and the
//     snapshot ops as RPC wrappers that drive the out-of-process VM plugin;
//   - the out-of-process VM plugin (candy/plugin-vm/, package main) wires the
//     in-process versions: a no-op egress (the host validates what the plugin
//     returns), its own embedded build_defaults.yml decoder, and the go-libvirt
//     snapshot implementations.
//
// Each consumer assigns these in an init() (see vmshared_aliases.go in each
// module), following the codebase's swappable package-level var idiom
// (cf. checkvars.go's InspectContainer). They are called only at runtime — never
// during this package's own var initialization (the OVMF tables that read the
// embedded vocab are computed lazily in ovmf_paths.go, after the consumer's
// init() has run) — so a missing assignment surfaces as an immediate nil-call,
// never a silent default.

// ValidateEgress gates a generated cloud-init document against its CUE egress
// schema before the bytes are emitted (RenderCloudInit in cloud_init_render.go).
var ValidateEgress func(kind, label string, data []byte) error

// UnmarshalEmbeddedDefaults decodes the consumer's embedded build vocabulary
// (the ovmf_paths / ovmf_distro_aliases directives the OVMF resolver reads) into
// dst. Core reads its embedded charly.yml; the plugin reads its embedded
// build_defaults.yml.
var UnmarshalEmbeddedDefaults func(dst any)

// Snapshot backends. Core wires host-side RPC wrappers (vm_snapshot_client.go)
// that drive the out-of-process plugin; the plugin wires the in-process
// go-libvirt implementations (vm_snapshot_internal.go / vm_snapshot_libvirt.go).
var (
	CreateInternalSnapshot    func(opts SnapshotCreateOpts) error
	DeleteInternalSnapshot    func(vmName string, entry *SnapshotEntry) error
	RevertInternalSnapshot    func(vmName string, entry *SnapshotEntry) error
	PromoteInternalToExternal func(vmName string, entry *SnapshotEntry, outPath string) error
	CreateExternalSnapshot    func(opts SnapshotCreateOpts, outFile string) error
	DeleteExternalSnapshot    func(vmName string, entry *SnapshotEntry) error
	RevertExternalSnapshot    func(vmName string, entry *SnapshotEntry) error
)
