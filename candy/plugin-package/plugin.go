// Package pkgverb is the importable, COMPILED-IN host-coupled `package` verb: the
// TYPED-STEP state-provision verb (Go package pkgverb — `package` is a keyword). Three
// roles on the charly/plugin/kit contract:
//   - CheckVerbProvider: rpm -q / dpkg -s / pacman -Q probe + optional version match.
//   - ProvisionActor (runtime act): render the dnf/apt-get/pacman install shell.
//   - StepProvider (build/deploy act): lower into a SystemPackagesStep (the host
//     materializer resolves the format + cross-distro name, keeps Reverse() in package
//     main). Relocated out of charly's module (formerly charly/plugin/builtins/package +
//     charly/plugin_verb_package.go); COMPILED-IN-ONLY. The cross-distro name resolver is
//     the shared kit.ResolvePackageName (R3).
package pkgverb

import (
	"context"
	"embed"
	"fmt"
	"slices"
	"strings"

	"github.com/opencharly/charly/candy/plugin-package/params"
	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:package": "#PackageInput"}

// NewCheckVerb returns the package verb as a kit.CheckVerbProvider for compiled-in
// registration. Because verb also implements kit.ProvisionActor + kit.StepProvider, charly
// registers the three-role (check + act + typed-step) adapter.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "package" }

// RunVerb (do:assert) probes installed/version via rpm/dpkg/pacman through the live
// CheckContext. Mirrors r.runPackage.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.PackageInput
	kit.DecodeInput(op.PluginInput, &in)
	wantInstalled := true
	if in.Installed != nil {
		wantInstalled = *in.Installed
	}
	name := kit.ResolvePackageName(in.Package, in.PackageMap, cc.Distros())
	pkgQ := kit.ShellQuote(name)
	probe := fmt.Sprintf(
		`rpm -q %[1]s >/dev/null 2>&1 || (dpkg -s %[1]s 2>/dev/null | grep -q "^Status:.*install ok installed") || pacman -Q %[1]s >/dev/null 2>&1`,
		pkgQ)
	_, stderr, exit, err := cc.Exec().RunCapture(ctx, probe)
	if err != nil {
		return kit.Failf("probe failed: %v (%s)", err, stderr)
	}
	isInstalled := exit == 0
	if isInstalled != wantInstalled {
		return kit.Failf("installed=%v, want %v", isInstalled, wantInstalled)
	}
	if !isInstalled {
		return kit.Pass("absent (as expected)")
	}
	if len(in.Versions) > 0 {
		versionProbe := fmt.Sprintf(
			`rpm -q --qf '%%{VERSION}\n' %[1]s 2>/dev/null || dpkg -s %[1]s 2>/dev/null | awk '/^Version:/{print $2; exit}' || pacman -Q %[1]s 2>/dev/null | awk '{print $2}'`,
			pkgQ)
		ver, _, exit, err := cc.Exec().RunCapture(ctx, versionProbe)
		if err != nil || exit != 0 {
			return kit.Failf("version probe exit %d err %v", exit, err)
		}
		got := strings.TrimSpace(ver)
		if !slices.Contains(in.Versions, got) {
			return kit.Failf("version %q not in %v", got, in.Versions)
		}
	}
	return kit.Pass("installed")
}

// RenderProvisionScript (do:act runtime) renders the install under whichever package
// manager the live target runs. ok is always true. Mirrors the former
// packageVerb.RenderProvisionScript.
func (verb) RenderProvisionScript(op *spec.Op, distros []string) (string, bool) {
	var in params.PackageInput
	kit.DecodeInput(op.PluginInput, &in)
	name := kit.ShellQuote(kit.ResolvePackageName(in.Package, in.PackageMap, distros))
	return fmt.Sprintf(`if command -v dnf >/dev/null 2>&1; then dnf install -y %[1]s; `+
		`elif command -v apt-get >/dev/null 2>&1; then apt-get update && apt-get install -y %[1]s; `+
		`elif command -v pacman >/dev/null 2>&1; then pacman -S --noconfirm %[1]s; `+
		`else echo "no supported package manager" >&2; exit 1; fi`, name), true
}

// StepKind names the typed install-plan step package's build/deploy act lowers into.
func (verb) StepKind() kit.StepKindName { return kit.StepKindSystemPackages }

// ConstructStepDescriptor (do:act build/deploy) returns the authored package name + map;
// the host materializer resolves the cross-distro name + image format + builds the
// SystemPackagesStep (Repos/Copr/Options come from the top-level package cascade, not here).
func (verb) ConstructStepDescriptor(op *spec.Op) kit.StepDescriptor {
	var in params.PackageInput
	kit.DecodeInput(op.PluginInput, &in)
	return kit.StepDescriptor{SystemPackages: &kit.SystemPackagesDesc{Package: in.Package, PackageMap: in.PackageMap}}
}
