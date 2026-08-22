package spec

// Backend returns the localhost backend port for a tunneled port, defaulting to
// Port when BackendPort is unset (0). Pure structural method on the generated
// spec.TunnelPort wire type (schema/tunnel.cue) — the ONE home (R3) for the
// backend-port default the pod quadlet emitter (sdk/deploykit) consumes. Lives
// here beside the type it acts on (the charly_methods.go convention): touches
// ONLY the type's own fields + the stdlib.
func (tp TunnelPort) Backend() int {
	if tp.BackendPort != 0 {
		return tp.BackendPort
	}
	return tp.Port
}
