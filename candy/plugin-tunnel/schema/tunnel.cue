// The OUT-OF-TREE plugin-tunnel's OWN CUE schema — the typed params for the
// `tunnel` VERB (verb:tunnel), the externalized tailscale/cloudflare TUNNEL
// EXECUTION LEG. It is the SINGLE SOURCE for this plugin's params, used two ways
// (the same contract core `spec` + the reference examplerunverb use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct (params.TunnelInput / TunnelConfig / TunnelPort), never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin SERVES this source over the
//     Describe channel (sdk.BuildCapabilities → schema_cue); the host splices it onto
//     its base (base ++ plugin) and validates every authored `plugin: tunnel` step's
//     plugin_input against #TunnelInput. The host NEVER reads this file from disk — the
//     schema travels with the plugin.
//
// verb:tunnel is NOT a check verb in the usual sense — it is the externalized TUNNEL
// EXECUTION backend. charly's core tunnel_plugin.go forwards TunnelStart / TunnelStop /
// cloudflareTunnelSetup over this verb's Invoke envelope ({method,config}). It ALSO
// carries a benign `plan` (dry-run) method — a `plugin: tunnel` check step returns the
// EXACT tailscale/cloudflared argv it WOULD run WITHOUT exec, so a disposable bed proves
// the registry dispatch + the TunnelConfig wire round-trip + the moved command-building
// logic with ZERO tailscale/cloudflare credentials.
//
// SELF-CONTAINED: #TunnelInput references only its sibling defs (#TunnelConfig /
// #TunnelPort) WITHIN this schema, NO base def, so it compiles standalone (gengotypes +
// the SDK serve-side check) AND splices onto the base — the splice exists to detect a
// def-name collision with the base, not to resolve base references.

// #TunnelInput — the plugin_input shape for the `tunnel` verb.
#TunnelInput: {
	// method — the tunnel operation: start | stop | setup | plan. `plan` is the
	// creds-free dry-run that returns the argv the operation WOULD run (no exec).
	method: string & !="" @go(Method)
	// config — the resolved tunnel configuration to act on (byte-compatible with the
	// core's TunnelConfig, sent by tunnel_plugin.go over the Invoke envelope).
	config?: #TunnelConfig @go(Config)
	// expect — plan only: the expected argv command lines (space-joined). When set, the
	// plan method compares the built argv against it (FAIL on mismatch); empty ⇒ the
	// step passes as long as dispatch + round-trip succeed, echoing the built argv.
	expect?: [...string] @go(Expect)
}

// #TunnelConfig — the resolved, ready-to-execute tunnel configuration.
#TunnelConfig: {
	// provider — "tailscale" or "cloudflare".
	provider?: string @go(Provider)
	// tunnel_name — cloudflare: tunnel name.
	tunnel_name?: string @go(TunnelName)
	// hostname — cloudflare: default hostname (from the image dns field).
	hostname?: string @go(Hostname)
	// box_name — for PID file naming / cloudflare tunnel-name default.
	box_name?: string @go(BoxName)
	// ports — all tunneled ports with their access scope.
	ports?: [...#TunnelPort] @go(Ports)
}

// #TunnelPort — a single port to tunnel with its protocol and access scope.
#TunnelPort: {
	// port — the tailscale HTTPS listen port (must be a valid serve/funnel port).
	port?: int @go(Port)
	// backend_port — the localhost backend port (0 means same as port).
	backend_port?: int @go(BackendPort)
	// protocol — backend scheme: http | https | https+insecure | tcp |
	// tls-terminated-tcp | ssh | rdp | smb (udp is skipped, never tunneled).
	protocol?: string @go(Protocol)
	// public — true = internet-accessible (funnel), false = private (tailnet-only serve).
	public?: bool @go(Public)
	// hostname — cloudflare: per-port hostname (from the map form).
	hostname?: string @go(Hostname)
}
