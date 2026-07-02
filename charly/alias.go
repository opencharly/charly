package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// aliasNameRe matches valid alias names: starts with alphanumeric, allows dots/underscores/hyphens
var aliasNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

const aliasMarker = "# charly-alias"

// generateAliasScript produces the wrapper script content for a host command alias.
// The wrapper builds a properly quoted command string and calls charly shell -c.
func generateAliasScript(box, command string) string {
	return fmt.Sprintf(`#!/bin/sh
# charly-alias
# box: %s
# command: %s
_charly_q(){ printf "'"; printf '%%s' "$1" | sed "s/'/'\\\\''/g"; printf "' "; }
c="%s"; for a in "$@"; do c="$c $(_charly_q "$a")"; done
exec charly shell %s -c "$c"
`, box, command, command, box)
}

// writeAliasScript writes a wrapper script to dir/name with mode 0755.
func writeAliasScript(dir, name, image, command string) error {
	path := filepath.Join(dir, name)
	content := generateAliasScript(image, command)
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("writing alias script %s: %w", path, err)
	}
	return nil
}

// removeAliasScript verifies the file has the charly-alias marker, then deletes it.
func removeAliasScript(dir, name string) error {
	path := filepath.Join(dir, name)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("alias %q not found in %s", name, dir)
		}
		return err
	}

	if !strings.Contains(string(data), aliasMarker) {
		return fmt.Errorf("%s is not an charly alias (missing marker)", path)
	}

	return os.Remove(path)
}

// AliasInfo holds parsed metadata from a wrapper script.
type AliasInfo struct {
	Name    string
	Box     string
	Command string
}

// listAliasScripts scans dir for files with the charly-alias marker and returns their metadata.
func listAliasScripts(dir string) ([]AliasInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var aliases []AliasInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		info, err := parseAliasScript(path)
		if err != nil || info == nil {
			continue
		}
		info.Name = entry.Name()
		aliases = append(aliases, *info)
	}

	return aliases, nil
}

// parseAliasScript reads a file and extracts alias metadata if it has the marker.
func parseAliasScript(path string) (*AliasInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	scanner := bufio.NewScanner(f)
	var hasMarker bool
	var image, command string

	for scanner.Scan() {
		line := scanner.Text()
		if line == aliasMarker {
			hasMarker = true
		}
		if after, ok := strings.CutPrefix(line, "# box: "); ok {
			image = after
		}
		if after, ok := strings.CutPrefix(line, "# command: "); ok {
			command = after
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parsing alias script %s: %w", path, err)
	}

	if !hasMarker {
		return nil, nil
	}

	return &AliasInfo{Box: image, Command: command}, nil
}

// CollectedAlias represents a resolved alias ready for installation.
type CollectedAlias struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// CollectBoxAlias gathers aliases from the box's own candies + box-level config.
// No base chain traversal — aliases are leaf-box specific.
// Candy aliases come first; box-level overrides by name.
func CollectBoxAlias(cfg *Config, layers map[string]*Candy, boxName string) ([]CollectedAlias, error) {
	img, ok := cfg.Box[boxName]
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

// defaultAliasDir returns ~/.local/bin, creating it if needed.
func defaultAliasDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.Getenv("HOME"), ".local", "bin")
	}
	return filepath.Join(home, ".local", "bin")
}

// --- CLI Commands ---

// AliasCmd groups alias subcommands
type AliasCmd struct {
	Add       AliasAddCmd       `cmd:"" help:"Create a host command alias"`
	Install   AliasInstallCmd   `cmd:"" help:"Install default aliases from the candy + box config"`
	List      AliasListCmd      `cmd:"" help:"List all installed aliases"`
	Remove    AliasRemoveCmd    `cmd:"" help:"Remove an alias"`
	Uninstall AliasUninstallCmd `cmd:"" help:"Remove all aliases for a box"`
}

// AliasAddCmd creates a single alias
type AliasAddCmd struct {
	Name    string `arg:"" help:"Alias name (command on host)"`
	Box     string `arg:"" help:"Box name from charly.yml"`
	Command string `arg:"" optional:"" help:"Command inside container (default: alias name)"`
	Dest    string `long:"dest" default:"" help:"Directory for wrapper scripts (default: ~/.local/bin)"`
}

func (c *AliasAddCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Validate the image exists locally. If not, surface the standard
	// "charly box pull" recommendation via ErrImageNotLocal.
	if !LocalImageExists(rt.RunEngine, c.Box) {
		return fmt.Errorf("%w: %s", ErrImageNotLocal, c.Box)
	}

	command := c.Command
	if command == "" {
		command = c.Name
	}

	dest := c.Dest
	if dest == "" {
		dest = defaultAliasDir()
	}

	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dest, err)
	}

	if err := writeAliasScript(dest, c.Name, c.Box, command); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Created alias %s -> %s (box: %s)\n", c.Name, command, c.Box)
	return nil
}

// AliasRemoveCmd removes a single alias
type AliasRemoveCmd struct {
	Name string `arg:"" help:"Alias name to remove"`
	Dest string `long:"dest" default:"" help:"Directory for wrapper scripts (default: ~/.local/bin)"`
}

func (c *AliasRemoveCmd) Run() error {
	dest := c.Dest
	if dest == "" {
		dest = defaultAliasDir()
	}

	if err := removeAliasScript(dest, c.Name); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Removed alias %s\n", c.Name)
	return nil
}

// AliasListCmd lists all installed aliases
type AliasListCmd struct {
	Dest string `long:"dest" default:"" help:"Directory for wrapper scripts (default: ~/.local/bin)"`
}

func (c *AliasListCmd) Run() error {
	dest := c.Dest
	if dest == "" {
		dest = defaultAliasDir()
	}

	aliases, err := listAliasScripts(dest)
	if err != nil {
		return err
	}

	for _, a := range aliases {
		fmt.Printf("%s\t%s\t%s\n", a.Name, a.Box, a.Command)
	}
	return nil
}

// AliasInstallCmd installs all default aliases for an image
type AliasInstallCmd struct {
	Box  string `arg:"" help:"Box name from charly.yml"`
	Dest string `long:"dest" default:"" help:"Directory for wrapper scripts (default: ~/.local/bin)"`
}

func (c *AliasInstallCmd) Run() error {
	// Read aliases from image labels.
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	imageRef := resolveShellImageRef("", c.Box, "")
	runEngine := ResolveBoxEngineForDeploy(c.Box, "", rt.RunEngine)
	meta, err := ExtractMetadata(runEngine, imageRef)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("image %s has no embedded metadata; rebuild with latest charly", imageRef)
	}
	aliases := meta.Alias

	if len(aliases) == 0 {
		fmt.Fprintf(os.Stderr, "No aliases defined for image %s\n", c.Box)
		return nil
	}

	dest := c.Dest
	if dest == "" {
		dest = defaultAliasDir()
	}

	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dest, err)
	}

	for _, a := range aliases {
		if err := writeAliasScript(dest, a.Name, c.Box, a.Command); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Installed %s -> %s\n", a.Name, a.Command)
	}

	fmt.Fprintf(os.Stderr, "Installed %d alias(es) for %s\n", len(aliases), c.Box)
	return nil
}

// AliasUninstallCmd removes all aliases for an image
type AliasUninstallCmd struct {
	Box  string `arg:"" help:"Box name from charly.yml"`
	Dest string `long:"dest" default:"" help:"Directory for wrapper scripts (default: ~/.local/bin)"`
}

func (c *AliasUninstallCmd) Run() error {
	dest := c.Dest
	if dest == "" {
		dest = defaultAliasDir()
	}

	aliases, err := listAliasScripts(dest)
	if err != nil {
		return err
	}

	count := 0
	for _, a := range aliases {
		if a.Box == c.Box {
			path := filepath.Join(dest, a.Name)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing %s: %w", path, err)
			}
			fmt.Fprintf(os.Stderr, "Removed %s\n", a.Name)
			count++
		}
	}

	fmt.Fprintf(os.Stderr, "Removed %d alias(es) for %s\n", count, c.Box)
	return nil
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
