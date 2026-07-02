// CUE schema for the `module` kind (Calamares installer module / module.desc).
// #Module validates ONE module entity. CLOSED: every authored key is modeled (an
// unknown key is a typo). No on-disk corpus yet; modeled from ModuleSpec.
// `requiredModules` keeps Calamares' camelCase verbatim. No #Step.

#Module: {
	name:         string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	description?: string & !=""
	type?:        "job" | "view"
	interface?:   "qtplugin" | "python" | "process"
	load?:        string & !=""
	script?:      string & !=""
	command?: [...(string & !="")]
	requiredModules?: [...(string & !="")]
	weight?:   int & >=0     @go(,type=int)
	noconfig:  *false | bool @go(NoConfig)
	emergency: *false | bool
	timeout?:  int & >=0 @go(,type=int)
	chroot:    *false | bool
}
