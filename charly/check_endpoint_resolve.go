package main

import "context"

// check_endpoint_resolve.go — the generic host-endpoint reverse-leg (H part 2). ONE
// class-generic venue→host-reachable-addr resolution serves BOTH the in-process
// runnerCheckContext and the out-of-process CheckContextService, REPLACING the per-verb
// host preresolvers (cdp/vnc/spice/mcp each declared their in-venue port and wrapped this
// SAME resolveCheckVenue + resolveCheckEndpoint machinery). The plugin decides WHAT to
// resolve (its port); the host holds the machinery the out-of-process plugin cannot reach.

// resolveVerbEndpoint resolves the current check target's venue (container / VM / ssh /
// local) and returns a host-reachable "host:port" for an in-venue TCP port, registering
// any opened ssh -L forward (VM/ssh venue) for post-Invoke teardown. Empty addr + nil err
// = no live venue (box-mode / no-box) — the verb's own no-endpoint skip then fires.
func (r *Runner) resolveVerbEndpoint(port int) (string, error) {
	if r.Box == "" || r.Mode == RunModeBox {
		return "", nil
	}
	venue, err := resolveCheckVenue(r.Box, r.Instance)
	if err != nil {
		return "", err
	}
	ep, err := resolveCheckEndpoint(venue, port)
	if err != nil {
		return "", err
	}
	r.endpointCleanups = append(r.endpointCleanups, ep.Close)
	return ep.Addr, nil
}

// runEndpointCleanups closes every forward opened during the verb's Invoke (LIFO) and
// resets the list. Called by invokeVerbProvider after the Invoke returns — the forward must
// outlive the plugin's dial, so it is released only once the Invoke completes.
func (r *Runner) runEndpointCleanups() {
	for i := len(r.endpointCleanups) - 1; i >= 0; i-- {
		if r.endpointCleanups[i] != nil {
			r.endpointCleanups[i]()
		}
	}
	r.endpointCleanups = nil
}

// ResolveEndpoint is the in-process CheckContext leg — a compiled-in kit verb reaches the
// SAME generic endpoint resolution an out-of-process one gets over CheckContextService.
func (c runnerCheckContext) ResolveEndpoint(_ context.Context, port int) (string, error) {
	return c.r.resolveVerbEndpoint(port)
}
