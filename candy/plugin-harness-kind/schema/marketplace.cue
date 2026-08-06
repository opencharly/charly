// schema/marketplace.cue — the SELF-CONTAINED CUE schema validating the `marketplace` KIND's
// authored VALUE (the single harness/marketplace config entity per repo). Self-contained per the
// group/agent reproduction contract — the same authored wire keys as the core #Marketplace
// (spec/schema/marketplace.cue, the single source that generates spec.Marketplace); the host
// validates against #MarketplaceInput at load and this plugin's Invoke canonicalises through
// spec.Marketplace.
#MarketplaceInput: close({
	name:        string & =~"^[a-z][a-z0-9-]*$"         // "charly-plugins" (the marketplace name)
	version:     string & =~"^[0-9]+[.][0-9]+[.][0-9]+$" // marketplace.json metadata.version
	description?: string & !=""                          // marketplace.json metadata.description
	families: {[string]: #MarketplaceFamilyInput}    // family name → its metadata (plugins/ dir = family)
	settings?: #MarketplaceSettingsInput              // the harness wiring data
})
#MarketplaceFamilyInput: close({
	category?:    *"images" | "commands" | "kind" | "development" // the README four-bucket classification
	description?: string & !=""                                    // plugin.json + marketplace.json description
	keywords?:    [...(string & !="")]                             // marketplace.json keywords
	version?:     string & =~"^[0-9]+[.][0-9]+[.][0-9]+$"          // plugin.json version (default: family candy CalVer)
	profiles?:    [...("developer" | "user" | "container")]        // profiles.json membership
	mcp_servers?: [...#MarketplaceMCPServerInput]                  // plugins/<family>/.mcp.json entries
})
#MarketplaceMCPServerInput: close({
	name:    string & !=""
	type?:   *"http" | "stdio"
	url?:    string & !=""      // http type (e.g. http://localhost:8888/mcp)
	command?: string & !=""     // stdio type (e.g. github-mcp-server)
	args?:    [...(string & !="")]
})
#MarketplaceSettingsInput: close({
	enabled_plugins?: [...(string & !="")]          // .claude/settings.json enabledPlugins (charly-*)
	source_path?:     *"./plugins" | string & !=""  // extraKnownMarketplaces.<name>.source.path
	hooks?:           [...string]                   // hook entity names to wire into settings.json
})
