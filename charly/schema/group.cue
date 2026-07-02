// CUE entity shape for the Calamares-style netinstall package group. #Group is no
// longer a registered core kind — the `package-group:` kind was extracted into a
// dedicated plugin unit (candy/plugin-package-group), which ships its OWN
// self-contained #PackageGroupInput reproduction for host-side input validation.
// #Group remains the canonical core entity TYPE (it generates spec.Group), the
// decode/consumer reference the plugin's Invoke returns and a consumer reads back.
// No on-disk corpus yet. RECURSIVE via subgroup. CLOSED. #PackageItem /
// #DistroPackages / #AUR / #RepoBlock are shared (_common.cue). No #Step.

#Group: {
	name:         #EntityRef
	description?: string & !=""
	package?: [...#PackageItem]
	distro?: {[string]: #DistroPackages} @go(Distro,type=map[string]*DistroPackages)
	hidden:        *false | bool
	selected:      *false | bool
	critical:      *false | bool
	immutable:     *false | bool
	expanded:      *false | bool
	noncheckable:  *false | bool @go(NonCheckable)
	pre_install?:  string & !="" @go(PreInstall)
	post_install?: string & !="" @go(PostInstall)
	source?:       string & !=""
	subgroup?: [...#Group]
	require?: [...(string & !="")]
}
