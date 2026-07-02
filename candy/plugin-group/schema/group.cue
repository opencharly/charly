// schema/group.cue — the SELF-CONTAINED CUE schema validating the `group` KIND's authored VALUE:
// the TARGETLESS deploy-group config. Ships over Describe (schema_cue); references NO base def so it
// compiles standalone (BuildCapabilities compiles it alone, failing loudly if broken) AND splices onto
// the base (the base ++ plugin splice detects a def-name collision, not resolves base refs).
//
// This is a PURPOSE-BUILT targetless-group value surface — the identity + lifecycle fields a group
// (no own workload) carries — NOT a clone of the full #Deploy: the workload fields (image/from/port/
// env/volume/security/secret/sidecar/tunnel/route/probes/storage/expose/replica/…) are MEANINGLESS on
// a targetless group and are now correctly rejected (the former builtin validated the value against the
// whole #Deploy, so those fields were accepted-but-ignored; rejecting them is stricter + correct — no
// real group authors them). The authored MEMBER children ride op.Env (host-pre-decoded, F5
// authored-member input-threading), NOT this value. The complex lifecycle sub-objects
// (ephemeral/preemptible) are PERMISSIVE here — validate_ephemeral validates them on the folded
// uf.Bundle entry (the real deploy-level gate), and plan steps are validated by validateOps there too —
// so this def stays small, self-contained, and drift-free (no #Iterate/#Ephemeral/#Deploy clone).
#GroupInput: {
	// identity + description
	version?:     string
	description?: string
	// disposability + lifecycle (the fields real groups author: disposable/lifecycle/description)
	disposable?: bool
	lifecycle?:  string
	// lifecycle sub-objects — permissive (validated by validate_ephemeral on the folded Bundle)
	ephemeral?:   {...}
	preemptible?: {...}
	// exclusive/shared host-resource arbitration on a group
	requires_exclusive?: [...string]
	requires_shared?: [...string]
	// iterate — the AI-benchmark harness on a group bed (a folded data child, e.g. charly-cli).
	// Permissive here — validateIterateBed validates it on the folded uf.Bundle entry (the real gate).
	iterate?: {...}
	// direct plan steps on the group node (checks are usually on members; permitted + validated by
	// validateOps on the folded Bundle entry)
	plan?: [...{...}]
}
