package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEveryPackageFormatWithContainerInstallAlsoDeclaresHost is the control for the host-venue axis of a
// distro format's `phase.install` — the axis its sibling tests never crossed. TestOCITargetSkipsApkInstall
// covers the OCI axis (OCI targets SKIP the apk install) and TestResolveCheckApk covers the check-verb APK
// resolution; neither asserts that a host venue has an install cell to run at all.
//
// FAILS without the change: before it, the committed manifest's `apk:` format declared a `container:` cell
// and no `host:` cell, so a `target: local` (host) venue installed NOTHING — silently — while `deploy del`
// still recorded a package-remove op. The assertion is structural (every format that installs in a
// container must also install on a host), so it cannot be satisfied by a stray `host:` elsewhere in the file.
//
// It reads the COMMITTED manifest rather than a testdata fixture, because the committed one is what ships.
func TestEveryPackageFormatWithContainerInstallAlsoDeclaresHost(t *testing.T) {
	dir := ".."
	if _, err := os.Stat(filepath.Join(dir, "charly.yml")); err != nil {
		t.Skipf("cannot locate the committed charly.yml from %s", mustGetwd(t))
	}
	distroCfg, _, _, err := LoadBuildConfigForBox(dir)
	if err != nil {
		t.Fatalf("loading %s: %v", dir, err)
	}
	if distroCfg == nil || len(distroCfg.Distro) == 0 {
		t.Fatalf("%s declared no distros", dir)
	}
	checked, skipped := 0, []string{}
	for dname, d := range distroCfg.Distro {
		for fname, f := range d.Format {
			if f == nil || f.Phases == nil || f.Phases.Install == nil {
				continue
			}
			// A format with no container install cell is not a package-installing format; skip it.
			if f.Phases.Install.Container == "" {
				continue
			}
			// A `secondary: true` format is an adjunct to a primary one (aur rides alongside pac) and is
			// applied where its primary applies. Whether a host venue supports it is a separate contract
			// question -- recorded on opencharly/charly#772 rather than decided by this assertion, which
			// would otherwise force an invented host-side AUR build path just to go green.
			if f.Secondary {
				skipped = append(skipped, dname+"/"+fname)
				continue
			}
			checked++
			if f.Phases.Install.Host == "" {
				t.Errorf("distro %q format %q declares a container: install cell but NO host: cell — "+
					"on a host venue (target: local) it installs nothing, silently, while `deploy del` still "+
					"records a package-remove op. Mirror the deb/rpm cells.", dname, fname)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("the assertion never ran: no format in %s declared a container: install cell", dir)
	}
	t.Logf("checked %d container-install format(s); skipped %d secondary format(s) %v",
		checked, len(skipped), skipped)
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		return "?"
	}
	return wd
}
