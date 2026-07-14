package alias

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

const aliasMarker = "# charly-alias"

// hostClient is the alias command's ONE host coupling: it reaches charly's host process over the
// generic HostBuild("cli") reverse channel (the same seam the pod/vm lifecycles use) to run
// `charly box labels …` — the only image facts `add`/`install` need. Every other subcommand is a
// pure ~/.local/bin wrapper-script operation with no host dependency.
type hostClient struct {
	ctx  context.Context
	exec *sdk.Executor
}

// cli asks the HOST to run `charly <argv>` via the generic "cli" host-builder and returns the
// CliReply (stdout when capture, the process exit code, any spawn error).
func (h *hostClient) cli(capture, bestEffort bool, argv ...string) (spec.CliReply, error) {
	reqJSON, err := json.Marshal(spec.CliRequest{Argv: argv, Capture: capture, BestEffort: bestEffort})
	if err != nil {
		return spec.CliReply{}, err
	}
	resJSON, err := h.exec.HostBuild(h.ctx, "cli", reqJSON)
	if err != nil {
		return spec.CliReply{}, err
	}
	var r spec.CliReply
	if uerr := json.Unmarshal(resJSON, &r); uerr != nil {
		return spec.CliReply{}, uerr
	}
	return r, nil
}

// imageExists reports whether the box image is in local container storage, via `charly box labels
// <box>` (which resolves the ref against local storage and fails when the image is absent).
func (h *hostClient) imageExists(box string) (bool, error) {
	r, err := h.cli(true, true, "box", "labels", box)
	if err != nil {
		return false, err
	}
	return r.ExitCode == 0, nil
}

// collectAliases reads the box image's baked ai.opencharly.alias label (a JSON []CollectedAlias)
// via `charly box labels <box> --format alias`. An absent label (no aliases) exits non-zero and
// yields an empty slice — the caller distinguishes "image missing" earlier via imageExists.
func (h *hostClient) collectAliases(box string) ([]collectedAlias, error) {
	r, err := h.cli(true, true, "box", "labels", box, "--format", "alias")
	if err != nil {
		return nil, err
	}
	out := strings.TrimSpace(r.Stdout)
	if r.ExitCode != 0 || out == "" {
		return nil, nil
	}
	var aliases []collectedAlias
	if uerr := json.Unmarshal([]byte(out), &aliases); uerr != nil {
		return nil, fmt.Errorf("parsing ai.opencharly.alias label of %s: %w", box, uerr)
	}
	return aliases, nil
}

// collectedAlias mirrors charly's baked ai.opencharly.alias label entry (name + host command).
type collectedAlias struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

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

// defaultAliasDir returns ~/.local/bin, creating it if needed.
func defaultAliasDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.Getenv("HOME"), ".local", "bin")
	}
	return filepath.Join(home, ".local", "bin")
}

// dispatchAliasCLI kong-parses the pass-through args into the AliasCmd tree and runs the selected
// leaf, binding the hostClient so the `add`/`install` handlers can reach the host reverse channel.
func dispatchAliasCLI(hc *hostClient, args []string) error {
	var cli AliasCmd
	return sdk.RunInProcCLI("alias", &cli, args, kong.Bind(hc))
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

func (c *AliasAddCmd) Run(hc *hostClient) error {
	// Validate the image exists locally. If not, surface the standard
	// "charly box pull" recommendation.
	exists, err := hc.imageExists(c.Box)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("image %q is not in local storage (run `charly box pull %s` first)", c.Box, c.Box)
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

func (c *AliasInstallCmd) Run(hc *hostClient) error {
	// Read aliases from the built image's baked ai.opencharly.alias label.
	exists, err := hc.imageExists(c.Box)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("image %q is not in local storage; rebuild or `charly box pull %s` first", c.Box, c.Box)
	}
	aliases, err := hc.collectAliases(c.Box)
	if err != nil {
		return err
	}

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
