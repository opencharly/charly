// CUE schema for the `target` kind (Calamares install target / settings.conf).
// #Target validates ONE target entity. CLOSED: every authored key is modeled (an
// unknown key is a typo). No on-disk corpus yet; modeled from TargetSpec.
// Hyphenated Calamares wire keys are quoted. No #Step.

#Target: {
	name:         string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	description?: string & !=""
	"modules-search"?: [...(string & !="")] @go(ModulesSearch)
	instance?: [...#TargetInstance] @go(Instances)
	sequence?:        #TargetSequence
	branding?:        string & !=""
	"prompt-install": *false | bool @go(PromptInstall)
	"dont-chroot":    *false | bool @go(DontChroot)
	"oem-setup":      *false | bool @go(OemSetup)
	"disable-cancel": *false | bool @go(DisableCancel)
	group?: [...(string & !="")]
	box?: [...(string & !="")]
}

#TargetInstance: {
	id:      string & !="" @go(ID)
	module:  string & !=""
	config?: string & !=""
}

#TargetSequence: {
	show?: [...(string & !="")]
	exec?: [...(string & !="")]
}
