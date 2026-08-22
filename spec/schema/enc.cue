// enc.cue — the encrypted-volume (gocryptfs) EXECUTION wire types shared
// between charly's core and the compiled-in candy/plugin-enc (C16a; SDD
// conversion, per the standing operator directive: a hand-written wire struct
// not yet CUE-sourced is conversion-in-progress, never a sanctioned exception).
// The 4 method-selector string CONSTANTS (EncMethod*) are NOT wire-type structs
// — plain named string literals never carry a JSON/YAML shape for `cue exp
// gengotypes` to generate — so they stay hand-written in spec/enc_consts.go,
// mirroring the existing spec/gpu_consts.go / spec/arbiter_consts.go precedent.
// The host HOST-PRELIFTS everything that is NOT a gocryptfs shell command — the
// config loader (loadEncryptedVolume), the per-volume resolved paths +
// mount/init state, and the resolved passphrase (the credential store) — into
// an EncExecInput; the plugin runs ONLY the gocryptfs / systemd-run / fusermount3
// mechanics. Plain structs — gengotypes generates them faithfully, no
// disjunction needed.

// #EncVolumePlan is one encrypted volume, fully resolved HOST-SIDE: its charly
// name (for messages), the on-disk cipher/plain dirs, the systemd scope-unit
// name, and the host-probed initialized/mounted state. The plugin acts on
// these flags — it never re-derives a charly path convention nor re-probes
// state.
#EncVolumePlan: {
	name!:        string @go(Name)
	cipher_dir!:  string @go(CipherDir)
	plain_dir!:   string @go(PlainDir)
	scope_unit!:  string @go(ScopeUnit) // "charly-enc-<dir>-<name>" (no .scope suffix)
	initialized!: bool   @go(Initialized)
	mounted!:     bool   @go(Mounted)
}

// #EncExecInput is the self-contained gocryptfs-execution request the host
// ships to plugin-enc over OpExecute. ImageID is the systemd-ask-password /
// extpass id ("charly-<box>"); BoxName is the bare box name for remediation
// messages; Passphrase drives mount/ensure (gocryptfs init/mount via
// GOCRYPTFS_PASSWORD); OldPass/NewPass drive passwd. Volumes carries the
// host-resolved per-volume plan.
#EncExecInput: {
	method!:      string @go(Method)
	image_id!:    string @go(ImageID)
	box_name!:    string @go(BoxName)
	passphrase?:  string @go(Passphrase)
	old_pass?:    string @go(OldPass)
	new_pass?:    string @go(NewPass)
	volumes!: [...#EncVolumePlan] @go(Volumes)
}

// #EncExecReply is the execution verdict: Error == "" means success. The host
// shim turns a non-empty Error into a Go error.
#EncExecReply: {
	error!: string @go(Error)
}
