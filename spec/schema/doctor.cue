// doctor.cue — wire types for the externalized `charly doctor` command plugin
// (candy/plugin-doctor; SDD conversion, per the standing operator directive: a
// hand-written wire struct not yet CUE-sourced is conversion-in-progress, never a
// sanctioned exception). NOT authoring kinds (never in #Node/#Op) — pure generated
// host<->plugin wire structs. The command LOGIC (the whole host-dependency
// report: check list, verdicts, human/JSON formatting, exit code, distro
// detection, device-glob probing, and the pure host ops — binary/file probes it
// runs itself) lives entirely in the plugin (K5 seam-death: the former "hostprobe"
// HostBuild kind is GONE — GPU/VFIO detection now reaches candy/plugin-gpu's
// verb:gpu peer-to-peer via InvokeProvider, credential-store health reaches
// candy/plugin-secrets' verb:credential the same way, and the install-hint /
// device / distro data tables are the plugin's own embed). #CredentialHealth is
// the ONE wire type that survives: it is the shared verb:credential health-probe
// reply shape, still used by charly-core's credential_plugin.go (VNC/enc callers)
// and by candy/plugin-doctor's own peer InvokeProvider call.

// #CredentialHealth is the credential-store health snapshot. Rendered into the
// doctor "secret storage" checks by the plugin.
#CredentialHealth: {
	backend_name!:       string @go(BackendName)
	configured_backend!: string @go(ConfiguredBackend)
	keyring_available!:  bool   @go(KeyringAvailable)
	keyring_locked!:     bool   @go(KeyringLocked)
	plaintext_count!:    int    @go(PlaintextCount,type=int)
	no_session!:         bool   @go(NoSession)
	coll_err?:           string @go(CollErr)
	healthy_colls?: [...string] @go(HealthyColls)
	broken_colls?: [...string] @go(BrokenColls)
	index_total!: int @go(IndexTotal,type=int)
	index_missing?: [...string] @go(IndexMissing)
}
