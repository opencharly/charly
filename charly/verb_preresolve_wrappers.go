package main

import "encoding/json"

// verb_preresolve_wrappers.go — registers each host-resolved check verb's
// preresolver into the generic verbPreresolvers registry (verb_preresolve.go).
// Each thin adapter calls the verb's existing host-side resolution (which owns the
// podman / venue / go-libvirt machinery the out-of-process plugin lacks) and adapts
// it to the uniform verbPreresolver shape: the 4 endpoint verbs marshal their
// resolved *Env into the opaque CheckEnv.Substrate; kube rewrites the op's
// KubeContext (carried in params). This is the ONE wiring site — invokeVerbProvider
// stays verb-agnostic (the Uniform API Invariant).

func init() {
	// cdp resolves its own endpoint via the generic cc.ResolveEndpoint reverse-leg (H part 2)
	// — no host preresolver.
	registerVerbPreresolver("vnc", func(r *Runner, c *Op) (json.RawMessage, *Op, func(), *CheckResult) {
		env, cleanup, early := r.preresolveVncEndpoint(c)
		if early != nil {
			return nil, c, cleanup, early
		}
		var sub json.RawMessage
		if env != nil {
			if b, err := json.Marshal(env); err == nil {
				sub = b
			}
		}
		return sub, c, cleanup, nil
	})
	registerVerbPreresolver("mcp", func(r *Runner, c *Op) (json.RawMessage, *Op, func(), *CheckResult) {
		env, early := r.preresolveMcpEndpoint(c)
		if early != nil {
			return nil, c, nil, early
		}
		var sub json.RawMessage
		if env != nil {
			if b, err := json.Marshal(env); err == nil {
				sub = b
			}
		}
		return sub, c, nil, nil
	})
	// spice resolves its own endpoint via the generic cc.ResolveGraphicsEndpoint reverse-leg
	// (H part 2) — no host preresolver.
	registerVerbPreresolver("kube", func(_ *Runner, c *Op) (json.RawMessage, *Op, func(), *CheckResult) {
		// kube ships no Substrate — it rewrites the op's KubeContext host-side (an
		// out-of-process kube verb cannot reach core's findK8sSpec project loader).
		return nil, preresolveKubeCluster(c), nil, nil
	})
}
