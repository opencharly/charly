package main

import (
	"fmt"
	"os"
	"regexp"
)

// alias_collect.go holds the alias CORE residue that OUTLIVES the `charly alias …` CLI extraction
// (the command itself moved to candy/plugin-alias, command:alias). These are NOT the command — each
// is a distinct kernel mechanism the command extraction does not touch, owned by its own cutover:
//
//   - CollectBoxAlias / CollectedAlias — the BUILD-TIME collector: generate.go bakes the resolved
//     alias set into the image's ai.opencharly.alias OCI label, and labels.go carries it on
//     BoxMetadata.Alias (the OCI-label contract). `charly box inspect --aliases` prints it.
//   - aliasNameRe — the LOAD-TIME validation regex (validate.go rejects malformed alias names).
//   - ListAliasesCmd — the `charly box list-aliases` box-verb (ListCmd), which enumerates candies
//     that DECLARE aliases from the project (a build-mode config read, not the deploy-mode CLI).
//
// The `charly alias` command reads the BAKED label (via `charly box labels … --format alias`) and
// owns the wrapper-script logic; it shares none of the code below.

// aliasNameRe matches valid alias names: starts with alphanumeric, allows dots/underscores/hyphens
var aliasNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// CollectedAlias represents a resolved alias ready for installation.
type CollectedAlias struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// CollectBoxAlias gathers aliases from the box's own candies + box-level config.
// No base chain traversal — aliases are leaf-box specific.
// Candy aliases come first; box-level overrides by name.
func CollectBoxAlias(cfg *Config, layers map[string]*Candy, boxName string) ([]CollectedAlias, error) {
	img, ok := cfg.BoxConfig(boxName)
	if !ok {
		return nil, fmt.Errorf("box %q not found in charly.yml", boxName)
	}

	// Resolve candies for this box (leaf-specific — aliases do NOT inherit from
	// a base box; the shared boxDirectCandies walk).
	resolved, err := cfg.boxDirectCandies(layers, boxName)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var result []CollectedAlias

	// Collect from candies
	for _, candyName := range resolved {
		layer, ok := layers[candyName]
		if !ok || !layer.HasAliases() {
			continue
		}
		for _, a := range layer.Alias() {
			if seen[a.Name] {
				continue
			}
			seen[a.Name] = true
			result = append(result, CollectedAlias(a))
		}
	}

	// Collect from box config (overrides candy aliases with same name)
	for _, a := range img.Alias {
		cmd := a.Command
		if cmd == "" {
			cmd = a.Name
		}
		if seen[a.Name] {
			// Override: find and replace
			for i := range result {
				if result[i].Name == a.Name {
					result[i].Command = cmd
					break
				}
			}
		} else {
			seen[a.Name] = true
			result = append(result, CollectedAlias{Name: a.Name, Command: cmd})
		}
	}

	return result, nil
}

// ListAliasesCmd lists candies with alias declarations
type ListAliasesCmd struct{}

func (c *ListAliasesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	result := AliasCandy(layers)
	names := make([]string, 0, len(result))
	for _, layer := range result {
		names = append(names, layer.Name)
	}
	sortStrings(names)

	for _, name := range names {
		layer := layers[name]
		for _, a := range layer.Alias() {
			fmt.Printf("%s\t%s\t%s\n", name, a.Name, a.Command)
		}
	}
	return nil
}
