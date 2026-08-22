// CUE schema for the `distro` kind. #Distro validates ONE value of the `distro:`
// map (DistroDef) — the build vocabulary. CLOSED: every authored key is modeled
// (an unknown key is a typo). TEXT/TEMPLATE fields are Go text/template — plain
// `string`, never parsed. #CacheMount / #PhaseSet / #PhaseTemplates are shared
// (_common.cue).

#Distro: {
	inherits?:         string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	inherit_packages?: bool @go(InheritPackages)
	version?:          string & =~"^[0-9]+(\\.[0-9]+)*$"
	bootstrap?:        #Bootstrap
	workaround?: [...string] @go(Workarounds)
	format?: {[string]: #Format} @go(Format,type=map[string]*Format)
	base_user?:        #BaseUser @go(BaseUser,optional=nillable)
	pacstrap?:         #Pacstrap @go(Pacstrap,optional=nillable)
	debootstrap?:      #Debootstrap @go(Debootstrap,optional=nillable)
	alpine_bootstrap?: #AlpineBootstrap @go(AlpineBootstrap,optional=nillable)
	bootloader?:       #Bootloader @go(Bootloader,optional=nillable)
	dnf?:              #Dnf @go(Dnf,optional=nillable)
}

// install_cmd is the bootstrap command; ubuntu sets it to "" (kept WITHOUT
// `& !=""` so the empty-string base case validates).
#Bootstrap: {
	install_cmd: string @go(InstallCmd)
	package?: [...string]
	cache_mount?: [...#CacheMount] @go(CacheMount)
}

#Format: {
	cache_mount?: [...#CacheMount] @go(CacheMount)
	section_field?: {[string]: "list" | "list_of_maps"} @go(SectionFields)
	install_template?:   string    @go(InstallTemplate)
	uninstall_template?: string    @go(UninstallTemplate)
	phase?:              #PhaseSet @go(Phases,optional=nillable)
	validate?: [...#FormatRule]
	secondary?: bool
	local_pkg?: #LocalPkg @go(LocalPkg,optional=nillable)
}

#FormatRule: {
	field: string & !=""
	rule:  string & !=""
}

// #LocalPkg — the local_pkg INSTALL machinery only (the source-build fields
// pkg_glob/source_sentinel/build_template/dep_builder were removed with the
// pkg/ source-build cutover: the `charly generate-packages` plugin builds the
// package now, so the deploy-time + image-build paths only INSTALL the published
// package via install_template/download_template).
#LocalPkg: {
	install_template:   string & !="" @go(InstallTemplate)
	probe:              string & !=""
	download_template?: string @go(DownloadTemplate)
}

#Pacstrap: {
	base_package?: [...string] @go(BasePackages)
	keyring_init_cmd?: string @go(KeyringInitCmd)
	mirrorlist_url?:   string & =~"^https?://" @go(MirrorlistURL)
	extra_repo?: [...#PacstrapRepo] @go(ExtraRepos)
	runtime_pacman_conf?: string @go(RuntimePacmanConf)
}
#PacstrapRepo: {
	name:      string & !=""
	server:    string & =~"^https?://"
	siglevel?: string @go(SigLevel)
}
#Debootstrap: {
	suite?:      string
	mirror?:     string & =~"^https?://"
	variant?:    string
	components?: string
	include_package?: [...string] @go(IncludePackages)
	base_package?: [...string] @go(BasePackages)
	extra_repo?: [...#DebootstrapRepo] @go(ExtraRepos)
}
#DebootstrapRepo: {
	name:        string & !=""
	url:         string & =~"^https?://" @go(URL)
	suite?:      string
	components?: string
}
#AlpineBootstrap: {
	mirror_url?: string & =~"^https?://" @go(MirrorURL)
}
#Bootloader: {
	install_template?:   string @go(InstallTemplate)
	initramfs_template?: string @go(InitramfsTemplate)
	fstab_template?:     string @go(FstabTemplate)
}
#BaseUser: {
	name: string & !=""
	uid:  int & >=0 @go(UID,type=int)
	gid:  int & >=0 @go(GID,type=int)
	home: string & =~"^/"
}
#Dnf: {
	max_parallel_downloads?: int & >=1 @go(MaxParallelDownloads)
	fastestmirror?:          bool
}

// --- resolve-to-envelope wire type (Cutover M, the long pole; SDD conversion,
// per the standing operator directive: a hand-written wire struct not yet
// CUE-sourced is conversion-in-progress, never a sanctioned exception).
// candy/plugin-distro resolves an authored `distro:` build-vocabulary entity
// into a ResolvedDistro the kernel's build engine consumes without importing
// the concrete spec.Distro. Written out explicitly (not embedding #Distro) so
// every field's required/optional state is independently auditable against
// the former hand type. The host keeps RenderTemplate + the cache-mount vocab
// (per the plan); the plugin owns the distro KNOWLEDGE (schema/typed
// shape/validation). PrimaryFormat()/LocalPkgFormat() are pure Go METHODS —
// CUE cannot express them — and stay hand-written in spec/distro_methods.go
// (mirrors Op.Kind() in spec/charly_methods.go: a method, not a type).
#ResolvedDistro: {
	inherits?:         string @go(Inherits)
	inherit_packages?: bool   @go(InheritPackages)
	version?:          string @go(Version)
	bootstrap?:        #Bootstrap @go(Bootstrap)
	workaround?: [...string] @go(Workarounds)
	format?: {[string]: #Format} @go(Format,type=map[string]*Format)
	base_user?:        #BaseUser        @go(BaseUser,optional=nillable)
	pacstrap?:         #Pacstrap        @go(Pacstrap,optional=nillable)
	debootstrap?:      #Debootstrap     @go(Debootstrap,optional=nillable)
	alpine_bootstrap?: #AlpineBootstrap @go(AlpineBootstrap,optional=nillable)
	bootloader?:       #Bootloader      @go(Bootloader,optional=nillable)
	dnf?:              #Dnf             @go(Dnf,optional=nillable)
	raw?: bytes @go(Raw,type=RawBody)
}

// #DistroConfig is the `distro:` build-vocabulary container — the whole distro
// map keyed by distro name (charly/charly.yml's `distro:` section). #55 step
// 3-III: relocated from sdk/buildkit so every charly/plugin file that uses ONLY
// this value type drops its sdk/buildkit import for spec (the import-purity
// lever). Its vocabulary-resolution methods (ResolveDistro / ResolveInherits /
// AllFormatNames / FindFormat / ValidFormat / ExpandPackageInheritance) are pure
// Go METHODS on the type's own map — CUE cannot express them — and stay
// hand-written in spec/distro_config_methods.go (mirrors ResolvedDistro's
// PrimaryFormat in spec/distro_methods.go: a method, not a type).
#DistroConfig: {
	distro: {[string]: #ResolvedDistro} @go(Distro,type=map[string]*ResolvedDistro)
}

// #DistroResolveInput carries one opaque distro body to project.
#DistroResolveInput: {
	distro!: bytes @go(Distro,type=RawBody)
}

// #DistroResolveReply wraps the resolved distro.
#DistroResolveReply: {
	resolved?: #ResolvedDistro @go(Resolved,optional=nillable)
}
