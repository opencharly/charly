package spec

// enc_consts.go — the encrypted-volume (gocryptfs) method-selector string
// constants shared between charly's core and the compiled-in candy/plugin-enc
// (C16a; SDD conversion). These are NOT wire-type structs — plain named string
// literals never carry a JSON/YAML shape for `cue exp gengotypes` to generate —
// so they stay hand-written here, mirroring the existing spec/gpu_consts.go /
// spec/arbiter_consts.go precedent (a small hand-written consts file living
// beside its CUE-generated struct siblings). The struct types this domain uses
// (EncVolumePlan/EncExecInput/EncExecReply) are CUE-sourced at
// sdk/schema/enc.cue -> generated into cue_types_gen.go.

// EncMethod selects which gocryptfs operation plugin-enc runs.
const (
	EncMethodMount   = "mount"   // mount the not-yet-mounted volumes (init must already exist)
	EncMethodUnmount = "unmount" // fusermount3 -u + stop the scope unit
	EncMethodEnsure  = "ensure"  // auto-init then mount (charly start transparent setup)
	EncMethodPasswd  = "passwd"  // gocryptfs -passwd for every initialized volume
)
