package main

// Disposable + lifecycle + ephemeral classification for deploy-shaped configs.
//
// Three fields, two with clearly separated roles plus one operational
// counterpart:
//
//   disposable: <bool>    // LOAD-BEARING. Default false. Explicit opt-in.
//                         //   `true` authorizes `charly update <name>` to
//                         //   destroy + rebuild + restart unattended.
//   lifecycle: <tier>     // INFORMATIONAL ONLY. Free-form human tag
//                         //   (dev | qa | prod | custom-whatever).
//                         //   Has ZERO effect on disposability.
//   ephemeral: <block>    // OPERATIONAL MANDATE. Counterpart to
//                         //   disposable's authorization. Presence
//                         //   means "MUST be destroyed as soon as it
//                         //   isn't needed anymore". Implies
//                         //   `disposable: true` automatically (the
//                         //   one documented exception to anti-
//                         //   derivation, because ephemeral
//                         //   STRENGTHENS the contract).
//
//   preemptible: <list|block>  // RESOURCE-ARBITRATION axis (the fourth,
//                         //   fully ORTHOGONAL classification). Holder
//                         //   side: this deploy occupies the named
//                         //   exclusive host-resource token(s) and MAY
//                         //   be gracefully stopped to free them, but
//                         //   MUST be restarted afterward (disk + def
//                         //   preserved). The INVERSE of disposable
//                         //   ("you may pause me, but bring me back"
//                         //   vs "you may wipe me"). The claimant side
//                         //   is requires_exclusive: [token...]. The
//                         //   arbiter (charly/preempt.go) matches the two.
//
// Anti-derivation invariant (with one named exception):
//   `lifecycle: dev` does NOT imply `disposable: true` — lifecycle is
//   informational only.
//   `ephemeral: ...` DOES imply `disposable: true` — ephemeral is a
//   stronger guarantee, and "must be destroyed when not needed" can
//   only be honored if "may be destroyed" is also true. Setting
//   `ephemeral: true` together with `disposable: false` is a
//   contradiction; the loader rejects it with a clear error.
//   `preemptible: ...` implies NOTHING and is implied by nothing —
//   it is orthogonal to all three above. A deploy may be both
//   preemptible (the arbiter stops it) and disposable (R10 rebuilds
//   it); neither derives from the other.
//
// See /charly-internals:disposable for the schema + rationale, and CLAUDE.md
// R10 for the verification-loop implications.

// Classified is the small contract a config struct implements so the
// charly CLI can ask "are you disposable?" / "are you ephemeral?" /
// "what lifecycle tag do you carry?" without caring whether the
// underlying struct is VmSpec (vm.yml), BundleNode (charly.yml),
// or a per-instance override.
//
// IsDisposable returns true when the config carries `disposable: true`
// OR is ephemeral (load-bearing implication). IsEphemeral is the
// stricter check. IsPreemptible is independent of both — it reports the
// resource-arbitration holder side (`preemptible:` present with a
// non-empty `holds:`), and never derives from / to disposability.
type Classified interface {
	IsDisposable() bool
	IsEphemeral() bool
	IsPreemptible() bool
	LifecycleTag() string
}

// Canonical lifecycle tag names documented for operators. These are
// NOT enforced — the field is free-form string. They exist so skills
// + error messages can recommend a small vocabulary, and so `charly
// status --lifecycle <tier>` output is predictable when a project
// sticks to the vocabulary.
var CanonicalLifecycleTags = []string{
	"scratch", "dev", "test", "qa", "staging", "prod",
}

// Note: there is no `IsDisposableFields(disposable, lifecycle)` helper.
// There is no derivation to
// encode — the result would always be the `disposable` argument verbatim,
// and such a helper would obscure that. Callers read
// `node.Disposable` directly. See /charly-internals:disposable.
