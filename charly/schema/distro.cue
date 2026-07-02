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

#LocalPkg: {
	pkg_glob:           string & !="" @go(PkgGlob)
	source_sentinel:    string & !="" @go(SourceSentinel)
	build_template:     string & !="" @go(BuildTemplate)
	install_template:   string & !="" @go(InstallTemplate)
	probe:              string & !=""
	dep_builder?:       string @go(DepBuilder)
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
