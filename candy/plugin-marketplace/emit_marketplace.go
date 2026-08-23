package marketplace

import (
	"sort"
)

// emit_marketplace.go — the marketplace ROOT manifests: .claude-plugin/marketplace.json
// (the `charly-plugins` catalog the harness's extraKnownMarketplaces + the docs generator read),
// .agents/plugins/marketplace.json (the Codex CLI / AGENTS-framework catalog), and
// profiles.json (the developer/user/container_families membership the setup installer used).
// All derive entirely from the marketplace entity — the single source.

type marketplaceManifest struct {
	Name  string `json:"name"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
	Metadata struct {
		Description string `json:"description"`
		Version     string `json:"version"`
	} `json:"metadata"`
	Plugins []marketplacePluginEntry `json:"plugins"`
}

type marketplacePluginEntry struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description"`
	Author      struct {
		Name string `json:"name"`
	} `json:"author"`
	Homepage   string   `json:"homepage"`
	Repository string   `json:"repository"`
	License    string   `json:"license"`
	Keywords   []string `json:"keywords,omitempty"`
	Category   string   `json:"category"`
}

// agentsMarketplaceManifest is the Codex CLI / AGENTS-framework catalog shape: the same plugin
// list with explicit local sources (resolved against the marketplace repo root) and an
// installation policy.
type agentsMarketplaceManifest struct {
	Name      string               `json:"name"`
	Interface agentsInterface      `json:"interface"`
	Plugins   []agentsPluginEntry  `json:"plugins"`
}

type agentsInterface struct {
	DisplayName string `json:"displayName"`
}

type agentsPluginEntry struct {
	Name     string       `json:"name"`
	Source   agentsSource `json:"source"`
	Policy   agentsPolicy `json:"policy"`
	Category string       `json:"category"`
}

type agentsSource struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

type agentsPolicy struct {
	Installation   string `json:"installation"`
	Authentication string `json:"authentication"`
}

type profilesManifest struct {
	Developer         []string `json:"developer"`
	User              []string `json:"user"`
	ContainerFamilies []string `json:"container_families"`
}

func emitMarketplace(em emissions, ks *kindSet, families []family) {
	em["plugins/.claude-plugin/marketplace.json"] = mustJSON(buildMarketplace(ks, families))
	em["plugins/.agents/plugins/marketplace.json"] = mustJSON(buildAgentsMarketplace(ks, families))
	em["plugins/profiles.json"] = mustJSON(buildProfiles(families))
}

func buildAgentsMarketplace(ks *kindSet, families []family) agentsMarketplaceManifest {
	var m agentsMarketplaceManifest
	m.Name = ks.Marketplace.Name
	m.Interface.DisplayName = "OpenCharly"
	for _, f := range families {
		m.Plugins = append(m.Plugins, agentsPluginEntry{
			Name:   "charly-" + f.Name,
			Source: agentsSource{Source: "local", Path: "./" + f.Name},
			Policy: agentsPolicy{Installation: "INSTALLED_BY_DEFAULT", Authentication: "ON_INSTALL"},
			Category: firstNonEmpty(f.Meta.Category, "images"),
		})
	}
	return m
}

func buildMarketplace(ks *kindSet, families []family) marketplaceManifest {
	var m marketplaceManifest
	m.Name = ks.Marketplace.Name
	m.Owner.Name = pluginAuthor
	m.Metadata.Description = ks.Marketplace.Description
	m.Metadata.Version = ks.Marketplace.Version
	for _, f := range families {
		var e marketplacePluginEntry
		e.Name = "charly-" + f.Name
		e.Source = "./" + f.Name
		e.Description = f.Meta.Description
		e.Author.Name = pluginAuthor
		e.Homepage = "https://github.com/opencharly/charly"
		e.Repository = pluginRepository
		e.License = pluginLicense
		e.Keywords = f.Meta.Keywords
		e.Category = firstNonEmpty(f.Meta.Category, "images")
		m.Plugins = append(m.Plugins, e)
	}
	return m
}

func buildProfiles(families []family) profilesManifest {
	var p profilesManifest
	for _, f := range families {
		p.Developer = append(p.Developer, "charly-"+f.Name)
		for _, profile := range f.Meta.Profiles {
			switch profile {
			case "user":
				p.User = append(p.User, "charly-"+f.Name)
			case "container":
				p.ContainerFamilies = append(p.ContainerFamilies, f.Name)
			}
		}
	}
	sort.Strings(p.Developer)
	sort.Strings(p.User)
	sort.Strings(p.ContainerFamilies)
	return p
}
