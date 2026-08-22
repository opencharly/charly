package spec

import "sort"

// fleet_keys.go — deterministic fleet-map key ordering + the agent-provisioned venue predicate.
// Pure helpers over the fleet-tree spec types (FleetNode / UnifiedFile), relocated to the
// dedicated spec module (#55 2b Class A) so charly core + the loader-consuming plugins reach them
// without importing loaderkit. loaderkit re-exports them as forwarders for its own callers.

// SortedDeployKeys returns the deploy-map keys in deterministic (sorted) order.
func SortedDeployKeys(m map[string]FleetNode) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SortedMemberKeys returns the member keys of a node in deterministic order.
func SortedMemberKeys(members map[string]*FleetNode) []string {
	if len(members) == 0 {
		return nil
	}
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// VenueIsAgentProvisioned reports whether the deploy node named venue (a child or member anywhere in
// the fleet tree) is flagged AgentProvisioned — the ONE genuinely fleet-tree-coupled predicate the
// check-run preflight needs to skip an agent-provisioned image's local-storage ensure.
func VenueIsAgentProvisioned(uf *UnifiedFile, venue string) bool {
	if uf == nil || venue == "" {
		return false
	}
	var walk func(n *FleetNode) bool
	walk = func(n *FleetNode) bool {
		if n == nil {
			return false
		}
		for k, child := range n.Children {
			if k == venue && child.AgentProvisioned {
				return true
			}
			if walk(child) {
				return true
			}
		}
		for k, member := range n.Members {
			if k == venue && member.AgentProvisioned {
				return true
			}
			if walk(member) {
				return true
			}
		}
		return false
	}
	for _, name := range SortedDeployKeys(uf.Fleet) {
		node := uf.Fleet[name]
		if walk(&node) {
			return true
		}
	}
	return false
}
