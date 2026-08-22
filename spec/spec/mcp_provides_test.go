package spec

import "testing"

// TestPodAwareMCPProvides covers the same-deploy localhost rewrite + local-wins dedup that both
// charly's deploy-time provides injection and the mcp check verb rely on (moved here in H part 2).
func TestPodAwareMCPProvides(t *testing.T) {
	entries := []MCPProvideEntry{
		{Name: "self", URL: "http://charly-box:8080/mcp", Source: "box"},     // same-deploy → localhost
		{Name: "peer", URL: "http://charly-other:9090/mcp", Source: "other"}, // cross-deploy → unchanged
		{Name: "self", URL: "http://charly-other:8080/mcp", Source: "other"}, // dup name, cross → dropped (local wins)
	}
	got := PodAwareMCPProvides(entries, "box", "charly-box")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries (local-wins dedup), got %d: %+v", len(got), got)
	}
	// self: same-deploy, rewritten to localhost.
	if got[0].Name != "self" || got[0].URL != "http://localhost:8080/mcp" {
		t.Errorf("self entry: got %+v, want localhost rewrite", got[0])
	}
	// peer: cross-deploy, container hostname unchanged.
	if got[1].Name != "peer" || got[1].URL != "http://charly-other:9090/mcp" {
		t.Errorf("peer entry: got %+v, want unchanged", got[1])
	}
}
