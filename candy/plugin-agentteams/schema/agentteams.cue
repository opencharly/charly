// This compiled-in plugin's OWN CUE schema, served over the Describe channel — the
// typed plugin_input for the `agentteams` controller-probe check verb. It is the
// SINGLE SOURCE for this plugin's verb params, used two ways (the same contract
// core `spec` and the http plugin use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `agentteams:` step's plugin_input against
//     #AgentTeamsInput.
//
// The verb is HOST-BASED (the mcp pattern): the provider resolves the controller's
// in-venue :8090 to a host-routable address via the reverse channel
// (cc.ResolveEndpoint — a published port on the pod substrate, a live ssh -L
// forward on the vm substrate), pulls the admin SA token from the venue
// (/var/run/agentteams/cli-token) over the executor, and probes the controller
// with the SAME apiClient the `charly agentteams` command plugin uses (R3 — one
// REST surface covers the CLI and every bed). Only the genuinely SHARED step
// modifiers (timeout, the exit_status/stdout/stderr matchers, context, …) stay on
// core #Op, read off the step Op by the provider.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (the SDK's
// serve-side check + gengotypes) AND splices onto the base (base ++ plugin is a
// def-name collision check, not a base-reference resolver).

// #AgentTeamsInput is the `agentteams` verb's plugin_input: the method name plus
// its method-exclusive modifiers.
#AgentTeamsInput: {
	// method — the agentteams verb method name (the verb's PRIMARY input field, so
	// `agentteams: status` desugars to {method: "status"}).
	method: ("status" | "manager-running" | "worker-running" | "worker-list") @go(Method,type=string)
	// name — the worker/manager name. worker-running's target (default "bed-worker",
	// matching the beds' created Worker CR); manager-running's optional specific
	// manager (default: any manager reaching Running).
	name?: string
	// out — snapshot's output directory for the hydration bundle. A HOST path for
	// the verb (the verb is host-based — the mcp pattern); a local path for the
	// in-venue command. Default: /tmp/agentteams-snapshot (verb) /
	// ./agentteams-snapshot (command).
	out?: string
	// bundle — hydrate's bundle directory to apply back (workers → teams → humans
	// in dependency order) and restore the mirrored MinIO objects from.
	bundle?: string
	// no_minio — skip the MinIO object mirroring (snapshot) / restore (hydrate).
	no_minio?: bool @go(NoMinio,type=bool)
}
