package marketplace

// emit_catalogs.go — the additional per-harness catalogs the marketplace root ships:
//
//   - kimi.plugin.json — the Kimi Code plugin manifest. Kimi's plugin model is one-repo-per-plugin
//     (its marketplace entries take a local path, zip URL, or GitHub URL as the whole plugin
//     source), so the entire marketplace is ONE kimi plugin: every family's skills/ + agents/
//     dirs are listed as ./-paths inside the installed clone.
//   - package.json — the pi package manifest (the pi.dev package gallery is npm-based; a git
//     source is installed per-project from .pi/settings.json's packages array and reconciled to a
//     pinned ref). The `pi` key declares the skills resource; the `./*/skills` glob covers every
//     family at the marketplace root. The `pi-package` keyword is the gallery discoverability tag.
//
// Both are corpus-root manifests — they live at the marketplace repo root, never in charly.

// kimiPluginJSON is the kimi.plugin.json shape (kimi docs: "Plugin Manifest").
type kimiPluginJSON struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Skills      []string      `json:"skills,omitempty"`
	Agents      []string      `json:"agents,omitempty"`
	Interface   kimiInterface `json:"interface"`
}

type kimiInterface struct {
	DisplayName      string `json:"displayName"`
	ShortDescription string `json:"shortDescription"`
}

// piPackageJSON is the pi package manifest (pi docs: packages.md "Creating a Pi Package").
type piPackageJSON struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Keywords    []string   `json:"keywords"`
	Pi          piManifest `json:"pi"`
}

type piManifest struct {
	Skills []string `json:"skills"`
}

func emitCatalogs(em emissions, ks *kindSet, families []family) {
	em["plugins/kimi.plugin.json"] = mustJSON(buildKimiManifest(ks, families))
	em["plugins/package.json"] = mustJSON(buildPiPackage(ks))
}

func buildKimiManifest(ks *kindSet, families []family) kimiPluginJSON {
	m := kimiPluginJSON{
		Name:        "charly",
		Description: ks.Marketplace.Description,
		Interface: kimiInterface{
			DisplayName:      "OpenCharly",
			ShortDescription: ks.Marketplace.Description,
		},
	}
	for _, f := range families {
		var hasSkills, hasAgents bool
		for _, s := range f.Skills {
			if s.Type == "agent" {
				hasAgents = true
			} else {
				hasSkills = true
			}
		}
		if hasSkills {
			m.Skills = append(m.Skills, "./"+f.Name+"/skills")
		}
		if hasAgents {
			m.Agents = append(m.Agents, "./"+f.Name+"/agents")
		}
	}
	return m
}

func buildPiPackage(ks *kindSet) piPackageJSON {
	return piPackageJSON{
		Name:        "opencharly-marketplace",
		Description: ks.Marketplace.Description,
		Keywords:    []string{"pi-package"},
		Pi:          piManifest{Skills: []string{"./*/skills"}},
	}
}
