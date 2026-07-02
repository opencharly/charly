package spec

// enc_wire.go — the encrypted-volume (gocryptfs) EXECUTION wire types shared
// between charly's core (package main) and the compiled-in candy/plugin-enc (C16a).
//
// These types live in package spec — the ONE importable home — because BOTH the
// host (the in-core enc shim, charly/enc.go) AND the plugin (candy/plugin-enc, via
// the replace → ../../charly module edge) construct and exchange them across the
// OpExecute Invoke boundary. The host HOST-PRELIFTS everything that is NOT a
// gocryptfs shell command — the config loader (loadEncryptedVolume), the per-volume
// resolved paths + mount/init state (the deploy-model path/probe helpers that stay
// CORE because ResolveVolumeBacking/verifyBindMounts consume them synchronously),
// and the resolved passphrase (the credential store) — into an EncExecInput; the
// plugin runs ONLY the gocryptfs / systemd-run / fusermount3 mechanics. There is NO
// duplicate type for any of these concepts (R3).

// EncMethod selects which gocryptfs operation plugin-enc runs.
const (
	EncMethodMount   = "mount"   // mount the not-yet-mounted volumes (init must already exist)
	EncMethodUnmount = "unmount" // fusermount3 -u + stop the scope unit
	EncMethodEnsure  = "ensure"  // auto-init then mount (charly start transparent setup)
	EncMethodPasswd  = "passwd"  // gocryptfs -passwd for every initialized volume
)

// EncVolumePlan is one encrypted volume, fully resolved HOST-SIDE: its charly
// name (for messages), the on-disk cipher/plain dirs, the systemd scope-unit name,
// and the host-probed initialized/mounted state. The plugin acts on these flags —
// it never re-derives a charly path convention nor re-probes state.
type EncVolumePlan struct {
	Name        string `json:"name"`
	CipherDir   string `json:"cipher_dir"`
	PlainDir    string `json:"plain_dir"`
	ScopeUnit   string `json:"scope_unit"` // "charly-enc-<dir>-<name>" (no .scope suffix)
	Initialized bool   `json:"initialized"`
	Mounted     bool   `json:"mounted"`
}

// EncExecInput is the self-contained gocryptfs-execution request the host ships to
// plugin-enc over OpExecute. ImageID is the systemd-ask-password / extpass id
// ("charly-<box>"); BoxName is the bare box name for remediation messages;
// Passphrase drives mount/ensure (gocryptfs init/mount via GOCRYPTFS_PASSWORD);
// OldPass/NewPass drive passwd. Volumes carries the host-resolved per-volume plan.
type EncExecInput struct {
	Method     string          `json:"method"`
	ImageID    string          `json:"image_id"`
	BoxName    string          `json:"box_name"`
	Passphrase string          `json:"passphrase,omitempty"`
	OldPass    string          `json:"old_pass,omitempty"`
	NewPass    string          `json:"new_pass,omitempty"`
	Volumes    []EncVolumePlan `json:"volumes"`
}

// EncExecReply is the execution verdict: Error == "" means success. The host shim
// turns a non-empty Error into a Go error.
type EncExecReply struct {
	Error string `json:"error"`
}
