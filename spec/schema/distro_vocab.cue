// distro_vocab.cue — the SINGLE source for the distro id space and each id's traits.
//
// Everything that used to be a hand-written Go table or a hardcoded switch on a distro
// name derives from here via `task cue:gen`:
//
//   #Distros  →  spec.DistroFormats   (id → package format)   replaces hostenv.distroIDToFormat
//             →  spec.DistroSSHUnits  (id → OpenSSH service)  replaces vmshared.sshUnitForDistro
//             →  spec.DistroInits     (id → init system)      replaces `if distro == "alpine"`
//             →  spec.DistroIDs       (the id list)
//   #DistroID →  the closed field type a VM source's `distro:` is validated against
//
// A distro's traits are DATA. A Go switch on a distro name is the same duplication in a
// less checkable form: it cannot be validated, cannot be enumerated, and silently defaults
// for any id it does not name — which is exactly how a Debian-family guest came to be
// rendered with Arch package names and an sshd unit it does not have.
//
// Adding a distro is ONE entry here. Nothing else needs editing, and nothing may infer a
// trait from anything other than this table: not from an account name, not from an image
// URL, not from a source kind.

// @go(-): vocabulary-only. The traits reach Go as the four generated TABLES
// (DistroFormats / DistroSSHUnits / DistroInits / DistroIDs), which is all any
// consumer reads — projecting a `DistroTrait` struct and a `Distros` map as well
// would add exported contract-module API with zero callers.
#DistroTrait: {
	// format is the native package format. The four charly implements.
	format: "rpm" | "deb" | "pac" | "apk"
	// ssh_unit is the service name OpenSSH installs. Debian-family ships `ssh`;
	// everyone else ships `sshd`. Getting this wrong leaves a guest unreachable.
	ssh_unit: "ssh" | "sshd"
	// init is the guest's service manager. It selects HOW a service is enabled and
	// started, never a distro-specific command inline in Go.
	init: "systemd" | "openrc"
} @go(-)

#Distros: [string]: #DistroTrait
#Distros: { // @go(-) — see #DistroTrait
	fedora: {format: "rpm", ssh_unit: "sshd", init: "systemd"}
	rhel: {format: "rpm", ssh_unit: "sshd", init: "systemd"}
	centos: {format: "rpm", ssh_unit: "sshd", init: "systemd"}
	rocky: {format: "rpm", ssh_unit: "sshd", init: "systemd"}
	almalinux: {format: "rpm", ssh_unit: "sshd", init: "systemd"}

	// Debian-family: the OpenSSH service is `ssh`, and on releases where it is
	// socket-activated the socket carries the same stem.
	debian: {format: "deb", ssh_unit: "ssh", init: "systemd"}
	ubuntu: {format: "deb", ssh_unit: "ssh", init: "systemd"}

	arch: {format: "pac", ssh_unit: "sshd", init: "systemd"}
	archarm: {format: "pac", ssh_unit: "sshd", init: "systemd"}
	manjaro: {format: "pac", ssh_unit: "sshd", init: "systemd"}
	endeavouros: {format: "pac", ssh_unit: "sshd", init: "systemd"}
	cachyos: {format: "pac", ssh_unit: "sshd", init: "systemd"}

	// Alpine runs OpenRC, not systemd. This row is what makes that a first-class,
	// enumerable property instead of a branch on the string "alpine".
	alpine: {format: "apk", ssh_unit: "sshd", init: "openrc"}
} @go(-)

// #DistroID is the closed id vocabulary, DERIVED from #Distros' own keys so the two can
// never disagree. A VM source's `distro:` is validated against this.
#DistroID: or([for id, _ in #Distros {id}]) @go(-)
