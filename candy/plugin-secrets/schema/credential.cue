// The OUT-OF-TREE plugin-secrets' OWN CUE schema — the typed params for the
// `credential` store-backend VERB (verb:credential). It is the SINGLE SOURCE for
// this plugin's params, used two ways (the same contract the reference
// examplerunverb + core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes the credential operation
//     into a TYPED struct, never a hand-parsed map.
//  2. VALIDATE / non-empty-schema load gate — the host splices this onto the base
//     (base ++ plugin) at connect; verb:credential carries no AUTHORED plugin_input
//     (its params are an internal RPC the core credential adapter sends, NOT a plan
//     step), so it advertises an EMPTY InputDef and this schema exists to satisfy the
//     host's non-empty-schema load gate (mirrors candy/plugin-mcp's schema/mcp.cue).
//
// SELF-CONTAINED: every field is a bare primitive referencing NO base def, so it
// compiles standalone (gengotypes + the load-gate compile) AND splices onto the base
// — the splice exists to detect a def-name collision with the base, not to resolve refs.
//
// verb:credential is NOT a check verb — it is the externalized CREDENTIAL STORE
// BACKEND. The core's pluginCredentialStore (charly/credential_plugin.go) forwards
// every CredentialStore method (get/set/delete/list/name), the env-less resolve
// (resolve → {value, source}), the doctor health probe (health), and the keyring
// re-probe (reset) over this verb's Invoke envelope.
#CredentialInput: {
	// method — the store operation: get | set | delete | list | name | resolve | health | reset | await-unlock.
	method: string & !="" @go(Method)
	// service — the credential service namespace (e.g. "charly/secret", "charly/vnc").
	service?: string @go(Service)
	// key — the entry key within the service.
	key?: string @go(Key)
	// value — the value to store (set only).
	value?: string @go(Value)
}
