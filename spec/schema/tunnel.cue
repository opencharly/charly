// CUE schema for the RESOLVED tunnel WIRE type — the host-side carrier
// spec.TunnelConfig / spec.TunnelPort that the pod-lifecycle plugins (candy/plugin-deploy-pod's
// podTunnelOp for start/stop, candy/plugin-pod's podTunnelStop for remove) marshal directly over
// the verb:tunnel {method,config} Invoke envelope to candy/plugin-tunnel via InvokeProvider — the
// former core dispatch adapter is DELETED (Cutover B unit 2: every
// tunnel EXECUTION leg is plugin-driven now, none dispatched from core) — and that the pod quadlet
// emitter (sdk/deploykit) consumes to render the tailscale serve/funnel + cloudflare
// companion-unit directives.
//
// SINGLE-SOURCED here per the SDD wire-type mandate: `task cue:gen` produces the
// Go structs (spec.TunnelConfig / spec.TunnelPort); the former HAND-WRITTEN
// charly/tunnel.go mirror is DELETED. This repairs a pre-existing wire-mandate
// VIOLATION (a host↔plugin wire struct that was hand-maintained) surfaced during
// the P11 pod-config-write relocation — fixed in the same cutover (R2).
//
// plugin-tunnel keeps its OWN self-contained #TunnelConfig / #TunnelPort
// (candy/plugin-tunnel/schema/tunnel.cue): the per-plugin schema is deliberately
// standalone (it validates the plugin's AUTHORED input + serves over Describe),
// while this spec def is the HOST-side wire carrier — structurally-identical
// twins are the established "host-call wire contract" convention (like the
// single-cap wire files). NOT an authoring kind (never in #Node/#Op) — a pure
// generated wire param type, like #BuildStageContext (buildctx.cue). @go names
// match the Go field names the quadlet emitter + tunnel runtime reference; the
// json keys are the wire contract (byte-compatible with plugin-tunnel's params).

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
	port?: int @go(Port,type=int)
	// backend_port — the localhost backend port (0 means same as port).
	backend_port?: int @go(BackendPort,type=int)
	// protocol — backend scheme: http | https | https+insecure | tcp |
	// tls-terminated-tcp | ssh | rdp | smb (udp is skipped, never tunneled).
	protocol?: string @go(Protocol)
	// public — true = internet-accessible (funnel), false = private (serve).
	public?: bool @go(Public)
	// hostname — cloudflare: per-port hostname (from the map form).
	hostname?: string @go(Hostname)
}
