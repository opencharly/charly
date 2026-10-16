package main

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEveryPackageFormatWithContainerInstallAlsoDeclaresHost is the control for the host-venue axis of a
// distro format's `phase.install` — the axis its siblings never crossed.
// TestOCITargetSkipsApkInstall covers the OCI axis (OCI targets SKIP the apk install) and
// TestResolveCheckApk covers the check-verb APK resolution; neither asserts that a host venue has an
// install cell to run at all.
//
// FAILS without the change: before it, the committed manifest's `apk:` format declared a `container:` cell
// and no `host:` cell, so a `target: local` (host) venue installed NOTHING — silently — while `deploy del`
// still recorded a package-remove op. The assertion is structural (every non-secondary format that installs
// in a container must also install on a host), so a stray `host:` elsewhere in the file cannot satisfy it.
//
// HERMETIC, on purpose: it reads the COMMITTED manifest — the one that ships — but parses it directly
// instead of through LoadBuildConfigForBox, because that loader resolves every @github ref and reaches for
// the network. A control that hangs CI cannot report anything, which is a worse failure than the one it
// catches (R3: no test reaches for infrastructure; R8: a boundary runs live or skips visibly, never hangs).
func TestEveryPackageFormatWithContainerInstallAlsoDeclaresHost(t *testing.T) {
	path := "charly.yml" // the committed manifest sits beside this test (cwd is charly/charly/)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read the committed manifest %s: %v", path, err)
	}

	// Top level maps distro name -> node, but it also carries scalars and lists (providers, pacstrap,
	// ovmf_paths, ...), so each node is decoded into the distro shape individually and a non-distro
	// node simply does not match. No loader is used, so no @github ref is resolved and no network is
	// touched (R3) — while the SHIPPED manifest is still the file under test.
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &top); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	type installCells struct {
		Host      string `yaml:"host"`
		Container string `yaml:"container"`
	}
	type formatNode struct {
		Secondary *bool `yaml:"secondary"`
		Phase     struct {
			Install *installCells `yaml:"install"`
		} `yaml:"phase"`
	}

	checked, skipped, distros := 0, []string{}, 0
	for dname, node := range top {
		var d struct {
			Distro struct {
				Format map[string]formatNode `yaml:"format"`
			} `yaml:"distro"`
		}
		if err := node.Decode(&d); err != nil || d.Distro.Format == nil {
			continue
		}
		distros++
		for fname, f := range d.Distro.Format {
			if f.Phase.Install == nil || f.Phase.Install.Container == "" {
				continue // not a package-installing format
			}
			// A `secondary: true` format is an adjunct to a primary one (aur rides alongside pac).
			// Whether a host venue supports it is a separate contract question — recorded on
			// opencharly/charly#772 rather than decided here, which would otherwise force an invented
			// host-side AUR build path just to turn this assertion green.
			if f.Secondary != nil && *f.Secondary {
				skipped = append(skipped, dname+"/"+fname)
				continue
			}
			checked++
			if f.Phase.Install.Host == "" {
				t.Errorf("distro %q format %q declares a container: install cell but NO host: cell — "+
					"on a host venue (target: local) it installs nothing, silently, while `deploy del` still "+
					"records a package-remove op. Mirror the deb/rpm cells.", dname, fname)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("the assertion never ran: %d distro node(s) in %s, none declaring a container: install cell",
			distros, path)
	}
	t.Logf("checked %d container-install format(s) across %d distro(s); skipped %d secondary %v",
		checked, distros, len(skipped), skipped)
}
