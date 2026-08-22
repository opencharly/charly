// CUE schema for the `marketplace` KIND — the single harness/marketplace config entity per repo
// (the migration of plugins/.claude-plugin/marketplace.json + profiles.json + the harness wiring
// into candy config). A `marketplace:` node is a top-level entity in a candy-dir file (e.g.
// candy/charly-marketplace/charly.yml), discovered by the recursive `candy/` walk.
//
// families: one entry per plugin family (the plugins/ directory name). The generator emits
// plugins/<family>/.claude-plugin/plugin.json + .codex-plugin/plugin.json, plugins/<family>/.mcp.json
// (when mcp_servers is non-empty), the aggregated plugins/.claude-plugin/marketplace.json and
// plugins/profiles.json, and the .claude/settings.json plugin-owned keys.
#Marketplace: close({
	name:        string & =~"^[a-z][a-z0-9-]*$"         // "charly-plugins" (the marketplace name)
	version:     string & =~"^[0-9]+[.][0-9]+[.][0-9]+$" // marketplace.json metadata.version
	description?: string & !=""                          // marketplace.json metadata.description
	families: {[string]: #MarketplaceFamily} @go(Families,type=map[string]MarketplaceFamily) // family name → its metadata (plugins/ dir = family)
	settings?: #MarketplaceSettings                  // the harness wiring data (settings.json plugin-owned keys)
})
#MarketplaceFamily: close({
	category?:    *"images" | "commands" | "kind" | "development" // the README four-bucket classification
	description?: string & !=""                                    // plugin.json + marketplace.json description
	keywords?:    [...(string & !="")]                             // marketplace.json keywords
	version?:     string & =~"^[0-9]+[.][0-9]+[.][0-9]+$"          // plugin.json version (default: family candy CalVer)
	profiles?:    [...("developer" | "user" | "container")]        // profiles.json membership
	mcp_servers?: [...#MarketplaceMCPServer] @go(McpServers,type=[]MarketplaceMCPServer) // plugins/<family>/.mcp.json entries
})
#MarketplaceMCPServer: close({
	name:    string & !=""
	type?:   *"http" | "stdio"
	url?:    string & !=""      // http type (e.g. http://localhost:8888/mcp)
	command?: string & !=""     // stdio type (e.g. github-mcp-server)
	args?:    [...(string & !="")]
})
#MarketplaceSettings: close({
	// enabled_plugins — the .claude/settings.json enabledPlugins charly-* entries (each
	// "charly-<family>@<name>"); empty ⇒ enable every declared family.
	enabled_plugins?: [...(string & !="")] @go(EnabledPlugins,type=[]string)
	// source_path — extraKnownMarketplaces.<name>.source.path (default ./plugins).
	source_path?: *"./plugins" | string & !="" @go(SourcePath)
	// hooks — the hook entity names to wire into settings.json hooks.<trigger>.
	hooks?: [...string] @go(Hooks,type=[]string)
})
