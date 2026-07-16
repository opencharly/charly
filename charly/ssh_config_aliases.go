package main

import "github.com/opencharly/sdk/kit"

// ssh_config_aliases.go — the managed ssh-config machinery moved to sdk/kit (so the co-located vm
// deploy plugin publishes its own Host stanza in OpPrepareVenue). These zero-churn aliases keep every
// core call site (check_bed_run / check_cmd / check_venue / deploy_tree / vm / vm_create_spec)
// compiling unchanged; kit is the single source (R3).
type VmSshStanza = kit.VmSshStanza

var (
	VmSshAlias        = kit.VmSshAlias
	RemoveVmSshStanza = kit.RemoveVmSshStanza
)
