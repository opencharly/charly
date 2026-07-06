package vm

// libvirt_methods.go is the libvirt verb's method allowlist — the method→(subcommand-path +
// positional-args) dispatch data dispatchLibvirtVerb reads to drive the in-process LibvirtCmd
// Kong tree. The PosX builders + MethodSpec live in the shared sdk/kit. Required strings name
// plugin-INPUT keys (#LibvirtVerbInput wire names) — the per-verb fields left core #Op in the
// schema-compaction cutover — except `target`, the SHARED step-level #Op field the snapshot
// methods keep requiring.

import "github.com/opencharly/sdk/kit"

var libvirtMethods = map[string]kit.MethodSpec{
	// Top-level verbs
	"list":       {Path: []string{"libvirt", "list"}, SkipBox: true},
	"info":       {Path: []string{"libvirt", "info"}},
	"screenshot": {Path: []string{"libvirt", "screenshot"}, PosArgs: kit.PosArtifact, Artifact: true},
	"send-key":   {Path: []string{"libvirt", "send-key"}, Required: []string{"key"}, PosArgs: kit.PosKeyNameSplit},
	"passwd":     {Path: []string{"libvirt", "passwd"}, Required: []string{"text"}, PosArgs: kit.PosText},
	"qmp":        {Path: []string{"libvirt", "qmp"}, Required: []string{"text"}, PosArgs: kit.PosLibvirtQmp},
	"domain-xml": {Path: []string{"libvirt", "domain-xml"}},
	"console":    {Path: []string{"libvirt", "console"}},
	"events":     {Path: []string{"libvirt", "events"}},

	// qemu-guest-agent subgroup
	"guest/ping":       {Path: []string{"libvirt", "guest", "ping"}},
	"guest/info":       {Path: []string{"libvirt", "guest", "info"}},
	"guest/os-info":    {Path: []string{"libvirt", "guest", "os-info"}},
	"guest/time":       {Path: []string{"libvirt", "guest", "time"}},
	"guest/hostname":   {Path: []string{"libvirt", "guest", "hostname"}},
	"guest/users":      {Path: []string{"libvirt", "guest", "users"}},
	"guest/interfaces": {Path: []string{"libvirt", "guest", "interfaces"}},
	"guest/disks":      {Path: []string{"libvirt", "guest", "disks"}},
	"guest/fsinfo":     {Path: []string{"libvirt", "guest", "fsinfo"}},
	"guest/vcpus":      {Path: []string{"libvirt", "guest", "vcpus"}},
	"guest/exec":       {Path: []string{"libvirt", "guest", "exec"}, Required: []string{"command"}, PosArgs: kit.PosCommandFields},
	"guest/fstrim":     {Path: []string{"libvirt", "guest", "fstrim"}},

	// Snapshot subgroup — the SHARED step-level target: field holds the snapshot name.
	"snapshot/list":   {Path: []string{"libvirt", "snapshot", "list"}},
	"snapshot/create": {Path: []string{"libvirt", "snapshot", "create"}, Required: []string{"target"}, PosArgs: kit.PosTarget},
	"snapshot/info":   {Path: []string{"libvirt", "snapshot", "info"}, Required: []string{"target"}, PosArgs: kit.PosTarget},
	"snapshot/revert": {Path: []string{"libvirt", "snapshot", "revert"}, Required: []string{"target"}, PosArgs: kit.PosTarget},
	"snapshot/delete": {Path: []string{"libvirt", "snapshot", "delete"}, Required: []string{"target"}, PosArgs: kit.PosTarget},
}
