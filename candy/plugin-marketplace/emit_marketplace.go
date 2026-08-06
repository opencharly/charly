package marketplace

import (
	"sort"
)

// emit_marketplace.go — the marketplace ROOT manifests: plugins/.claude-plugin/marketplace.json
// (the `charly-plugins` catalog the harness's extraKnownMarketplaces + the docs generator read)
// and plugins/profiles.json (the developer/user/container_families membership the setup installer
// used). Both derive entirely from the marketplace entity — the single source.

type marketplaceManifest struct {
	Name     string           `json:"name"`
	Owner    struct{ Name string } `json:"owner"`
	Metadata struct {
		Description string `json:"description"`
		Version     string `json:"version"`
	} `json:"metadata"`
	Plugins []marketplacePluginEntry `json:"plugins"`
}

type marketplacePluginEntry struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	Description string   `json:"description"`
	Author      struct{ Name string } `json:"author"`
	Homepage    string   `json:"homepage"`
	Repository  string   `json:"repository"`
	License     string   `json:"license"`
	Keywords    []string `json:"keywords,omitempty"`
	Category    string   `json:"category"`
}

type profilesManifest struct {
	Developer        []string `json:"developer"`
	User             []string `json:"user"`
	ContainerFamilies []string `json:"container_families"`
}

func emitMarketplace(em emissions, ks *kindSet, families []family) {
	em["plugins/.claude-plugin/marketplace.json"] = mustJSON(buildMarketplace(ks, families))
	em["plugins/profiles.json"] = mustJSON(buildProfiles(families))
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
