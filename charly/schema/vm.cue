// CUE schema for the `vm` kind. #Vm validates ONE value of the `vm:` map
// (VmSpec). FULLY MODELED + CLOSED: every VmSpec field, the 5-arm #VmSource
// union, the structured #VmCloudInit and the ~54-subtype
// #LibvirtDomain tree are modeled and CLOSED — an unknown
// key is a typo. Genuine passthroughs stay typed-open: libvirt.snippets /
// libvirt.xml_passthrough (raw XML), cloud_init.extra (raw cloud-config),
// cloud_init.network.ethernets (cloud-init network-config v2), and every
// libvirt map[string]string Go field as `{[string]: string}`.
//
// Cross-rules (CUE-owned): firmware:uefi-secure ⇒ machine≠i440fx,
// ssh.port ⊕ ssh.port_auto, cpu.mode:custom ⇒ model required, hostdev pci ⇒ hex
// source domain/bus/slot/function. Shared #Step from _common.cue (R3).

#Vm: {
	source: #VmSource @go(Source,type=VmSource)

	disk_size?: string @go(DiskSize)
	ram?:       string
	cpu?:       int & >=1 @go(Cpus,type=int) // yaml key is singular `cpu` (VmSpec.Cpus yaml:"cpu")
	machine?:   "q35" | "virt" | "i440fx" | "pc"
	// firmware is REQUIRED-with-default (not optional): an if-guard can only
	// reference a field that always resolves to a concrete value, and an
	// OPTIONAL field — even one carrying a default — errors with "cannot
	// reference optional field" when absent. Required-with-default materializes
	// "bios" on omission (matching the Go empty→bios behavior) AND stays
	// referenceable by the uefi-secure cross-rule below.
	firmware: *"bios" | "uefi-insecure" | "uefi-secure"

	backend:   *"auto" | "libvirt" | "qemu"
	autostart: *false | true
	// autostart:true requires the libvirt backend (qemu has no persistent daemon).
	if autostart {
		backend: "auto" | "libvirt"
	}

	// Secure Boot needs Q35 SMM — i440fx can't supply it.
	// machine stays OPTIONAL (Go allows empty machine with uefi-secure), so the
	// constraint is `machine?: !=…`, not `machine: !=…` (the latter would force
	// machine present and false-reject the common omit-machine uefi-secure case).
	if firmware == "uefi-secure" {
		machine?: !="i440fx"
		// Secure Boot requires SMM: the renderer does NOT
		// auto-enable SMM for uefi-secure (buildDomainFeatures only sets it from an
		// explicit libvirt.features.smm), so the user MUST declare it. The `!`
		// required-field markers force libvirt/features/smm to be EXPLICITLY present
		// (a plain `smm: true` would auto-fill and silently pass) and pinned true.
		libvirt!: features!: smm!: true
	}

	network?:    #VmNetwork    @go(Network,optional=nillable)
	ssh?:        #VmSSH        @go(SSH,type=*VmSSH)
	cloud_init?: #VmCloudInit  @go(CloudInit,optional=nillable)
	libvirt?:    #LibvirtDomain @go(Libvirt,type=*LibvirtDomain)

	plan?: [...#Step]
	snapshot?: [...#VmSnapshot] @go(Snapshots)
}

// 5-way discriminated union on source.kind; each arm pins kind, requires its
// fields, forbids cross-branch fields via _|_, and is CLOSED (no trailing `...`)
// so an unmodeled key is a typo.
#VmSource:
	{
		kind:         "cloud_image"
		url:          string & !=""
		checksum?:    #VmChecksum
		cache?:       string
		base_user?:   string
		box?:         _|_
		transport?:   _|_
		rootfs?:      _|_
		root_size?:   _|_
		kernel_args?: _|_
	} | {
		kind:         "bootc"
		box:          string & !=""
		transport?:   "registry" | "containers-storage" | "oci" | "oci-archive"
		rootfs?:      "ext4" | "xfs" | "btrfs"
		root_size?:   string
		kernel_args?: string
		url?:         _|_
		checksum?:    _|_
		cache?:       _|_
	} | {
		kind:              "clone"
		from_vm:           string & !=""
		from_snapshot:     string & !=""
		cloud_init_clean?: bool
		url?:              _|_
		box?:              _|_
		libvirt_name?:     _|_
		disk_path?:        _|_
		disk_format?:      _|_
	} | {
		kind:            "imported"
		libvirt_name:    string & !=""
		disk_path:       string & !=""
		disk_format:     "qcow2" | "raw"
		adopted_at?:     string
		last_synced_at?: string
		url?:            _|_
		box?:            _|_
		from_vm?:        _|_
		from_snapshot?:  _|_
	} | {
		kind:           "bootstrap"
		builder:        string & !=""
		distro:         string & !=""
		builder_image?: string
		rootfs?:        "ext4" | "xfs" | "btrfs"
		root_size?:     string
		kernel_args?:   string
		package?: [...string]
			bootstrap_arch?:    string
			bootstrap_variant?: string
			url?:               _|_
			box?:               _|_
			transport?:         _|_
	} @go(-) // gengotypes: hand VmSource (spec/union_types.go) — flat discriminated struct

#VmChecksum: {
	type?:  "sha256"
	value?: string & =~"^[0-9a-fA-F]{64}$"
}

#VmNetwork: {
	model?:  string
	mode:    *"user" | "bridge" | "nat" | "network"
	bridge?: string
	mac?:    string @go(MAC)
	port_forwards?: [...(string & =~":")] @go(PortForwards)
	if mode == "bridge" {
		bridge: string & !=""
	}
}

#VmSSH: {
							user?:          string
							port?:          int & >=0 & <=65535
							port_auto?:     bool
							key_source?:    *"auto" | "generate" | "none" | (string & =~"^/")
							key_injection?: #VmKeyInjection
							// port and port_auto are mutually exclusive (PortAuto && Port>0 was the
							// error): port_auto is false/absent OR port is ≤0/absent. The
							// disjunction keeps the struct CLOSED — an embedded matchN would open it.
} & ({port_auto?: false} | {port?: int & <=0}) @go(-) // gengotypes: hand VmSSH (spec/union_types.go)

#VmKeyInjection: {
	smbios?:     "auto" | "enabled" | "disabled" @go(SMBIOS)
	cloud_init?: "auto" | "enabled" | "disabled" @go(CloudInit)
}

#VmSnapshot: {
	name:         string & !=""
	description?: string
	mode?:        *"external" | "internal"
	quiesce?:     bool
	from?:        string
}

// ---------------------------------------------------------------------------
// cloud_init: VmCloudInit. CLOSED. Genuine passthroughs:
// extra (raw cloud-config string) and network.ethernets (network-config v2,
// map[string]map[string]any → {[string]: {[string]: _}}).
// ---------------------------------------------------------------------------
#VmCloudInit: {
	hostname?: string
	timezone?: string
	locale?:   string
	users?: [...#VmCloudInitUser]
	package?: [...string]
	runcmd?: [...string] @go(RunCmd)
	bootcmd?: [...string] @go(BootCmd)
	write_files?: [...#VmCloudInitFile] @go(WriteFiles)
	network?:        #VmCloudInitNetwork @go(Network,optional=nillable)
	mirrors?:        #VmCloudInitMirrors @go(Mirrors,optional=nillable)
	charly_install?: #VmCharlyInstall @go(CharlyInstall,optional=nillable)
	extra?:          string           // raw cloud-config YAML escape hatch (verbatim passthrough)
}

#VmCloudInitUser: {
	name:  string & !="" // VmCloudInitUser.Name yaml:"name" — required
	sudo?: bool
	groups?: [...string]
	shell?:       string
	lock_passwd?: bool @go(LockPasswd,type=*bool)
}

#VmCloudInitFile: {
	path:      string & !="" // VmCloudInitFile.Path yaml:"path" — required
	content?:  string
	owner?:    string
	perms?:    string // cloud-init perms, e.g. "0644" — no Go validator, kept plain
	encoding?: string // "" | b64 | gz | gz+b64 — no Go validator, kept plain
}

#VmCloudInitNetwork: {
	version?: int @go(,type=int)
	// network-config v2 map[string]map[string]any — typed-open passthrough.
	ethernets?: {[string]: {[string]: _}}
}

#VmCloudInitMirrors: {
	apt?: [...string] @go(APT)
	dnf?: [...string] @go(DNF)
	pacman?: [...string]
}

#VmCharlyInstall: {
	// VmCharlyInstall has ONLY `strategy` (the vm-spec skill's url/checksum are
	// STALE — the Go struct dropped them). auto: scp host binary post-boot;
	// scp: explicit form; skip: user-managed.
	strategy?: *"auto" | "scp" | "skip"
}

// ---------------------------------------------------------------------------
// libvirt: LibvirtDomain (libvirt_yaml.go). CLOSED, every sub-type modeled as a
// #Libvirt<Name> def. Genuine passthroughs stay typed (NOT blanket `{...}`):
// snippets/xml_passthrough (raw XML) and every map[string]string Go field as
// `{[string]: string}`.
// ---------------------------------------------------------------------------
#LibvirtDomain: {
	snippets?: [...string] // raw XML strings (candy-composed) — typed passthrough
	xml_passthrough?:      string @go(XMLPassthrough) // verbatim libvirt XML fragment — typed passthrough
	features?:             #LibvirtFeatures @go(Features,optional=nillable)
	cpu?:                  #LibvirtCPU @go(CPU,optional=nillable)
	clock?:                #LibvirtClock @go(Clock,optional=nillable)
	memory_backing?:       #LibvirtMemoryBacking @go(MemoryBacking,optional=nillable)
	memtune?:              #LibvirtMemTune       @go(MemTune,optional=nillable)
	numatune?:             #LibvirtNUMATune      @go(NUMATune,optional=nillable)
	cputune?:              #LibvirtCPUTune       @go(CPUTune,optional=nillable)
	iothreads?:            int                   @go(IOThreads,type=int)
	devices?:              #LibvirtDevices @go(Devices,optional=nillable)
	seclabel?:             #LibvirtSecLabel       @go(SecLabel,optional=nillable)
	launch_security?:      #LibvirtLaunchSecurity @go(LaunchSecurity,optional=nillable)
	resource?:             #LibvirtResource @go(Resource,optional=nillable)
	sysinfo?:              #LibvirtSysInfo @go(SysInfo,optional=nillable)
}

#LibvirtFeatures: {
	acpi?:   bool           @go(ACPI,type=*bool)
	apic?:   bool           @go(APIC,type=*bool)
	pae?:    bool           @go(PAE,type=*bool)
	smm?:    bool           @go(SMM,type=*bool)
	hap?:    bool           @go(HAP,type=*bool)
	vmport?: bool           @go(VMPort,type=*bool)
	pmu?:    bool           @go(PMU,type=*bool)
	hyperv?: #LibvirtHyperV @go(HyperV,optional=nillable)
	kvm?:    #LibvirtKVM    @go(KVM,optional=nillable)
	ibs?:    string         @go(IBS)
}

// HyperV enlightenment toggles — all "on"/"off"-ish strings; no Go validator,
// kept plain string to avoid false-rejecting valid libvirt values.
#LibvirtHyperV: {
	relaxed?:         string
	vapic?:           string @go(VAPIC)
	spinlocks?:       #LibvirtSpinlocks @go(Spinlocks,optional=nillable)
	vpindex?:         string @go(VPIndex)
	runtime?:         string
	synic?:           string
	stimer?:          string @go(STimer)
	reset?:           string
	vendor_id?:       #LibvirtVendorID @go(VendorID,optional=nillable)
	frequencies?:     string
	reenlightenment?: string
	tlbflush?:        string @go(TLBFlush)
	ipi?:             string @go(IPI)
	evmcs?:           string @go(EVMCS)
}

#LibvirtSpinlocks: {
	state?:   string
	retries?: int @go(,type=int)
}

#LibvirtVendorID: {
	state?: string
	value?: string
}

#LibvirtKVM: {
	hidden?:          string
	hint_dedicated?:  string @go(HintDedicated)
	poll_control?:    string @go(PollControl)
	pv_ipi?:          string @go(PVIPI)
	dirty_ring_size?: int    @go(DirtyRingSize,type=int)
}

#LibvirtCPU: {
	// mode is REQUIRED-with-default (renderer default host-passthrough) so the
	// custom⇒model if-guard below can reference it (optional fields error when
	// absent). #LibvirtCPU only instantiates when `cpu:` is present.
	mode:        *"host-passthrough" | "host-model" | "custom"
	model?:      string
	check?:      string // none|partial|full — no Go validator, kept plain
	migratable?: string // on|off — no Go validator, kept plain
	topology?:   #LibvirtCPUTopology @go(Topology,optional=nillable)
	features?: [...#LibvirtCPUFeature]
	cache?: #LibvirtCPUCache @go(Cache,optional=nillable)
	numa?: [...#LibvirtNUMACell] @go(NUMA)
	// custom mode requires model.
	if mode == "custom" {
		model: string & !=""
	}
}

#LibvirtCPUTopology: {
	sockets?: int @go(,type=int)
	dies?:    int @go(,type=int)
	cores?:   int @go(,type=int)
	threads?: int @go(,type=int)
}

#LibvirtCPUFeature: {
	policy?: "force" | "require" | "optional" | "disable" | "forbid"
	name:    string & !="" // LibvirtCPUFeature.Name yaml:"name" — required
}

#LibvirtCPUCache: {
	mode?:  string // emulate|passthrough|disable — no Go validator, kept plain
	level?: int    @go(,type=int)
}

#LibvirtNUMACell: {
	id?:        int    @go(ID,type=int)
	cpus?:      string @go(CPUs)
	memory?:    string
	unit?:      string
	memaccess?: string @go(MemAccess)
}

#LibvirtClock: {
	offset?:     "utc" | "localtime" | "timezone" | "variable" | "absolute"
	timezone?:   string
	adjustment?: string
	basis?:      string
	timers?: [...#LibvirtTimer]
}

#LibvirtTimer: {
	name:        string & !="" // LibvirtTimer.Name yaml:"name" — required
	present?:    string
	track?:      string
	tickpolicy?: string @go(TickPolicy)
	frequency?:  int    @go(,type=int)
	mode?:       string
}

#LibvirtMemoryBacking: {
	hugepages?:    #LibvirtHugepages @go(Hugepages,optional=nillable)
	nosharepages?: bool   @go(NoSharepages,type=*bool)
	locked?:       bool   @go(,type=*bool)
	source?:       string // file|anonymous|memfd — no Go validator, kept plain
	access?:       string // shared|private — no Go validator, kept plain
	allocation?:   string // immediate|ondemand — no Go validator, kept plain
	discard?:      bool   @go(,type=*bool)
}

#LibvirtHugepages: {
	size?:    string
	nodeset?: string @go(NodeSet)
}

#LibvirtMemTune: {
	hard_limit?:      string @go(HardLimit)
	soft_limit?:      string @go(SoftLimit)
	swap_hard_limit?: string @go(SwapHardLimit)
	min_guarantee?:   string @go(MinGuarantee)
}

#LibvirtNUMATune: {
	memory?: #LibvirtNUMAMemory @go(Memory,optional=nillable)
	memnodes?: [...#LibvirtMemnode] @go(MemNodes)
}

#LibvirtNUMAMemory: {
	mode?:      string
	nodeset?:   string
	placement?: string
}

#LibvirtMemnode: {
	cellid?:  int @go(CellID,type=int)
	mode?:    string
	nodeset?: string
}

#LibvirtCPUTune: {
	shares?:          int @go(,type=int)
	period?:          int @go(,type=int)
	quota?:           int @go(,type=int)
	global_period?:   int @go(GlobalPeriod,type=int)
	global_quota?:    int @go(GlobalQuota,type=int)
	emulator_period?: int @go(EmulatorPeriod,type=int)
	emulator_quota?:  int @go(EmulatorQuota,type=int)
	iothread_period?: int @go(IOThreadPeriod,type=int)
	iothread_quota?:  int @go(IOThreadQuota,type=int)
	vcpupin?: [...#LibvirtVCPUPin] @go(VCPUPin)
	emulatorpin?: #LibvirtEmulatorPin @go(EmulatorPin,optional=nillable)
	iothreadpin?: [...#LibvirtIOThreadPin] @go(IOThreadPin)
}

#LibvirtVCPUPin: {
	vcpu:   int           @go(VCPU,type=int) // LibvirtVCPUPin.VCPU yaml:"vcpu" — required
	cpuset: string & !="" @go(CPUSet)        // LibvirtVCPUPin.CPUSet yaml:"cpuset" — required
}

#LibvirtEmulatorPin: {
	cpuset: string & !="" @go(CPUSet) // required
}

#LibvirtIOThreadPin: {
	iothread: int           @go(IOThread,type=int) // required
	cpuset:   string & !="" @go(CPUSet)            // required
}

#LibvirtDevices: {
	emulator?: string
	disks?: [...#LibvirtDisk]
	interfaces?: [...#LibvirtInterface]
	channels?: [...#LibvirtChannel]
	serial?: [...#LibvirtSerial]
	console?: [...#LibvirtConsole]
	parallel?: [...#LibvirtParallel]
	graphics?: [...#LibvirtGraphics]
	video?: [...#LibvirtVideo]
	audio?: [...#LibvirtAudio]
	sound?: [...#LibvirtSound]
	inputs?: [...#LibvirtInput]
	usb?: [...#LibvirtUSB] @go(USB)
	redirdev?: [...#LibvirtRedirDev] @go(RedirDev)
	hostdevs?: [...#LibvirtHostdev]
	filesystems?: [...#LibvirtFilesystem]
	rng?: [...#LibvirtRNG] @go(RNG)
	tpm?: [...#LibvirtTPM] @go(TPM)
	watchdog?: [...#LibvirtWatchdog]
	memballoon?: #LibvirtMemBalloon @go(MemBalloon,optional=nillable)
	shmem?: [...#LibvirtShmem]
	iommu?: #LibvirtIOMMU @go(IOMMU,optional=nillable)
	vsock?: #LibvirtVsock @go(Vsock,optional=nillable)
	panic?: [...#LibvirtPanic]
	smartcard?: [...#LibvirtSmartcard]
	hub?: [...#LibvirtHub]
}

#LibvirtDisk: {
	type?:   string // file|block|network|volume — no Go validator, kept plain
	device?: string
	source?: {[string]: string}
	target?: {[string]: string}
	driver?: {[string]: string}
	readonly?: bool @go(,type=*bool)
	serial?:   string
	wwn?:      string @go(WWN)
	boot?:     int    @go(,type=int)
}

#LibvirtInterface: {
	type?: string
	source?: {[string]: string}
	model?: string
	mac?:   string @go(MAC)
	mtu?:   int    @go(MTU,type=int)
	driver?: {[string]: string}
	boot?: int @go(,type=int)
	port_forwards?: [...#LibvirtPortForward] @go(PortForwards)
}

#LibvirtPortForward: {
	proto?: string
	start:  int @go(,type=int) // LibvirtPortForward.Start yaml:"start" — required
	to?:    int @go(,type=int)
}

#LibvirtChannel: {
	type?:   string
	name?:   string
	path?:   string
	source?: string // LibvirtChannel.Source is a scalar string (not a map)
}

#LibvirtSerial: {
	type?: string
	source?: {[string]: string}
	target?: {[string]: string}
}

#LibvirtConsole: {
	type?: string
	target?: {[string]: string}
}

#LibvirtParallel: {
	type?: string
	source?: {[string]: string}
	target?: {[string]: string}
}

#LibvirtGraphics: {
	type:      "vnc" | "spice" | "rdp" | "sdl" | "egl-headless" // required
	port?:     int                                              @go(,type=int)
	autoport?: string                                           @go(AutoPort)
	listen?:   #LibvirtListen                                   @go(Listen,type=LibvirtGraphicsListeners,optional=nillable)
	passwd?:   string
	keymap?:   string
	gl?:       string @go(GL)
}

// LibvirtGraphicsListeners union: scalar address | single map | list of maps.
#LibvirtListen: (string | #LibvirtListenOne | [...#LibvirtListenOne]) @go(-) // gengotypes: hand LibvirtGraphicsListeners
#LibvirtListenOne: {
	type?:    string
	address?: string
	network?: string
	socket?:  string
}

#LibvirtVideo: {
	model:    string & !="" // LibvirtVideo.Model required; "none" is valid
	vram?:    int           @go(VRAM,type=int)
	heads?:   int           @go(,type=int)
	accel3d?: bool          @go(Accel3D,type=*bool)
	primary?: bool          @go(,type=*bool)
}

#LibvirtAudio: {
	type?: string
	id?:   int @go(ID,type=int)
}

#LibvirtSound: {
	model: string & !="" // LibvirtSound.Model yaml:"model" — required
}

#LibvirtInput: {
	type: string & !="" // LibvirtInput.Type yaml:"type" — required
	bus?: string
}

#LibvirtUSB: {
	model?: string
	port?:  int @go(,type=int)
}

#LibvirtRedirDev: {
	bus?:  string
	type?: string
}

// PCI source address component: 0x-hex OR bare decimal (hexUintPtr accepts both).
#LibvirtPCIHex: string & =~"^(0[xX][0-9a-fA-F]+|[0-9]+)$"

#LibvirtHostdev: {
	type:     "pci" | "usb" | "scsi" | "mdev" // required
	mode?:    string
	managed?: "yes" | "no"
	source: {[string]: string} // LibvirtHostdev.Source yaml:"source" — required typed map
	rom?: {[string]: string}
	driver?: {[string]: string}
	// PCI passthrough requires hex source domain/bus/slot/function;
	// a malformed address silently drops <source>.
	if type == "pci" {
		source: {
			domain:   #LibvirtPCIHex
			bus:      #LibvirtPCIHex
			slot:     #LibvirtPCIHex
			function: #LibvirtPCIHex
			...
		}
		}
} @go(-) // gengotypes: hand LibvirtHostdev (spec/union_types.go) — the if-pci redefine degrades to `any`

#LibvirtFilesystem: {
	type?:       string
	driver?:     "virtiofs" | "9p" | "path"
	accessmode?: "passthrough" | "mapped" | "squash" @go(AccessMode)
	source:      string & !=""                       // required (host path)
	target:      string & !=""                       // required (guest mount tag)
	readonly?:   bool                                @go(,type=*bool)
	binary?: {[string]: string}
}

#LibvirtRNG: {
	model?:   string
	backend?: string
	rate?: {[string]: string}
}

#LibvirtTPM: {
	model?: string
	backend?: {[string]: string}
}

#LibvirtWatchdog: {
	model:   string & !="" // LibvirtWatchdog.Model yaml:"model" — required
	action?: string
}

#LibvirtMemBalloon: {
	model:        string & !="" // LibvirtMemBalloon.Model yaml:"model" — required
	autodeflate?: string
	stats?: {[string]: int}
}

#LibvirtShmem: {
	name:  string & !="" // LibvirtShmem.Name yaml:"name" — required
	role?: string
	model?: {[string]: string}
	size?: string
	server?: {[string]: string}
}

#LibvirtIOMMU: {
	model: string & !="" // LibvirtIOMMU.Model yaml:"model" — required
	driver?: {[string]: string}
}

#LibvirtVsock: {
	model?: string
	cid?: {[string]: string} @go(CID)
}

#LibvirtPanic: {
	model?: string
	address?: {[string]: string}
}

#LibvirtSmartcard: {
	mode?: string
	type?: string
}

#LibvirtHub: {
	type: string & !="" // LibvirtHub.Type yaml:"type" — required
}

#LibvirtSecLabel: {
	type?:       string
	model?:      string
	relabel?:    string
	label?:      string
	baselabel?:  string @go(BaseLabel)
	imagelabel?: string @go(ImageLabel)
}

#LibvirtLaunchSecurity: {
	type?:              "sev" | "sev-es" | "sev-snp" | "tdx"
	cbitpos?:           int @go(CBitPos,type=int)
	reduced_phys_bits?: int @go(ReducedPhysBits,type=int)
	policy?:            string
	dh_cert?:           string @go(DhCert)
	session?:           string
	kernel_hashes?:     string @go(KernelHashes)
}

#LibvirtResource: {
	partition?: string
	fibrechannel?: {[string]: string} @go(FibreChannel)
}

#LibvirtSysInfo: {
	type?: string
	bios?: {[string]: string} @go(BIOS)
	system?: {[string]: string}
	baseboard?: [...{[string]: string}] @go(BaseBoard)
	chassis?: {[string]: string}
	processor?: [...{[string]: string}]
	oem_strings?: [...string] @go(OEMStrings)
}
