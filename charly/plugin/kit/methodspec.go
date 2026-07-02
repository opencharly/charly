package kit

import (
	"strings"

	"github.com/opencharly/charly/charly/spec"
)

// methodspec.go is the method-allowlist + positional-arg contract an EXTERNAL verb
// plugin's NESTED CLI imports to build its `charly check <verb> <method>` argv. The
// canonical consumer is candy/plugin-vm's libvirt verb (libvirt_methods.go): it maps
// each method name to a kit.MethodSpec (the subcommand path, the required #Op modifiers,
// the positional-arg builder) and drives the in-process LibvirtCmd Kong tree from it.
//
// This is NOT live-verb machinery — the former compiled-in live-verb seam (the in-proc
// live-container verb method-contract interface + the host's subprocess dispatcher) was
// deleted when the live-verb externalization orphaned it. Only the importable argv contract
// a nested-CLI plugin still needs survives here.

// MethodSpec is one method's nested-CLI dispatch spec: the `charly check <verb> <method...>`
// subcommand path, the required #Op modifiers, the positional-arg builder, and the artifact
// / skip-box flags. A plugin's method allowlist is a map[string]MethodSpec; fields are
// exported so a candy module can author it.
type MethodSpec struct {
	// Path is the `charly check <verb> <method...>` subcommand path.
	Path []string
	// Required names the #Op modifier fields that must be set for this method.
	Required []string
	// PosArgs builds the positional args inserted after the image name, before -i.
	PosArgs func(c *spec.Op) []string
	// Artifact marks a state-dependent capture method (screenshot) whose produced
	// file is validated.
	Artifact bool
	// SkipBox = true means the verb targets a cluster/other non-image target, so the
	// usual image/deploy-name positional is NOT inserted.
	SkipBox bool
}

// ---------------------------------------------------------------------------
// positional-arg builders — the shared library a nested-CLI plugin's MethodSpec
// allowlist references. Each returns the positional args to insert AFTER the image
// name, BEFORE any -i instance flag. They never fail: required-modifier checks run
// before this.
// ---------------------------------------------------------------------------

func PosText(c *spec.Op) []string     { return []string{c.Text} }
func PosTarget(c *spec.Op) []string   { return []string{c.Target} }
func PosArtifact(c *spec.Op) []string { return []string{c.Artifact} }

// PosKeyNameSplit splits c.KeyName on whitespace (libvirt send-key: "ctrl alt F2" → 3 slots).
func PosKeyNameSplit(c *spec.Op) []string {
	return strings.Fields(c.KeyName)
}

// PosCommandFields splits c.Command into argv slots, prefixed with `--` so kong does not
// treat embedded -flags as its own (libvirt:guest/exec). For shell metachars use
// `command: "sh -c '<full>'"`.
func PosCommandFields(c *spec.Op) []string {
	fields := strings.Fields(c.Command)
	if len(fields) == 0 {
		return nil
	}
	out := make([]string, 0, len(fields)+1)
	out = append(out, "--")
	out = append(out, fields...)
	return out
}

// PosLibvirtQmp emits a QMP method name + optional JSON args (Text = method, Input = JSON).
func PosLibvirtQmp(c *spec.Op) []string {
	args := []string{c.Text}
	if c.Input != "" {
		args = append(args, c.Input)
	}
	return args
}
