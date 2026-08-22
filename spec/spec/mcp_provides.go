package spec

import "strings"

// MCPProvideEntry is a RESOLVED mcp_provide entry — a CandyMCPProvide (the raw baked label
// form) plus the Source deployment it came from. It is shared by charly's DEPLOY-time provides
// injection (config_image.go) AND the out-of-process mcp CHECK verb (candy/plugin-mcp), which
// both apply PodAwareMCPProvides; it lives here (ONE copy, R3) so it crosses the charly/plugin
// module boundary. charly aliases it as MCPProvideEntry; its GetName/GetSource satisfy charly's
// Named interface structurally.
type MCPProvideEntry struct {
	Name      string `yaml:"name" json:"name"`
	URL       string `yaml:"url" json:"url"`
	Transport string `yaml:"transport,omitempty" json:"transport,omitempty"`
	Source    string `yaml:"source" json:"source"`
}

// GetName / GetSource let MCPProvideEntry satisfy charly's Named interface (provides.go).
func (e MCPProvideEntry) GetName() string   { return e.Name }
func (e MCPProvideEntry) GetSource() string { return e.Source }

// PodAwareMCPProvides rewrites same-deployment entries' URLs to localhost (a peer MCP server in
// the SAME pod is reached over loopback, not its container-DNS name) and prefers a local entry
// over a cross-deployment one of the same name. consumerKey is the consuming deployment's box
// name; ctrName is its running container name. Pure — moved verbatim from charly/provides.go.
func PodAwareMCPProvides(entries []MCPProvideEntry, consumerKey, ctrName string) []MCPProvideEntry {
	var result []MCPProvideEntry
	seen := map[string]bool{} // name → true if a local entry was added
	// First pass: same-deploy entries with a localhost rewrite.
	for _, e := range entries {
		if e.Source == consumerKey {
			local := e
			local.URL = strings.ReplaceAll(e.URL, ctrName, "localhost")
			result = append(result, local)
			seen[e.Name] = true
		}
	}
	// Second pass: cross-deploy entries (skipped when a local entry of the same name exists).
	for _, e := range entries {
		if e.Source != consumerKey && !seen[e.Name] {
			result = append(result, e)
		}
	}
	return result
}
