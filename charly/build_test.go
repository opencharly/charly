package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"text/template"

	"github.com/opencharly/spec/spec"
)

// testPacstrapMicroarchRe / testRenderPacstrapExtraConf / testRenderRuntimePacmanConf are thin
// local ports of sdk/buildkit's pure, stdlib-only pacstrap renderers (build_helpers.go) — their
// own correctness is ALREADY directly covered by sdk/buildkit/build_helpers_test.go
// (TestRenderRuntimePacmanConf: nil/blank/verbatim/template/malformed-template cases). This
// file's OWN test is a white-box check of charly's OWN production box/cachyos distro config
// (LoadBuildConfigForBox), not of the renderers in isolation — mirroring the dedup already
// applied to this file's sibling TestHostPlatform/TestRenderPacstrapExtraConf removals.
var testPacstrapMicroarchRe = regexp.MustCompile(`x86_64_v[0-9]+`)

func testRenderPacstrapExtraConf(p *spec.Pacstrap) string {
	if p == nil || len(p.ExtraRepos) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var microarch []string
	for _, r := range p.ExtraRepos {
		for _, m := range testPacstrapMicroarchRe.FindAllString(r.Server, -1) {
			if !seen[m] {
				seen[m] = true
				microarch = append(microarch, m)
			}
		}
	}
	sort.Strings(microarch)

	var b strings.Builder
	if len(microarch) > 0 {
		fmt.Fprintf(&b, "[options]\nArchitecture = x86_64 %s\n", strings.Join(microarch, " "))
	}
	for _, r := range p.ExtraRepos {
		fmt.Fprintf(&b, "[%s]\nServer = %s\n", r.Name, r.Server)
		if r.SigLevel != "" {
			fmt.Fprintf(&b, "SigLevel = %s\n", r.SigLevel)
		}
	}
	return b.String()
}

func testRenderRuntimePacmanConf(p *spec.Pacstrap) (string, error) {
	if p == nil || strings.TrimSpace(p.RuntimePacmanConf) == "" {
		return "", nil
	}
	tmpl, err := template.New("runtime_pacman_conf").Parse(p.RuntimePacmanConf)
	if err != nil {
		return "", fmt.Errorf("parsing runtime_pacman_conf template: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, p); err != nil {
		return "", fmt.Errorf("rendering runtime_pacman_conf: %w", err)
	}
	return b.String(), nil
}

// TestFilterImages / TestFilterImagesUnknown / TestFilterImagesIncludesBuilder /
// TestFilterImagesIncludesBootstrapBuilder relocated to candy/plugin-fleet (#55 decoupling,
// Batch A) — they asserted deploykit.FilterBox directly, zero charly coupling.

// TestHostPlatform / TestRenderPacstrapExtraConf were removed as duplicates (K3
// cone2 test closure): both asserted buildkit.HostPlatform / buildkit.RenderPacstrapExtraConf
// directly against literal fixtures — zero charly coupling — and sdk/buildkit/build_helpers_test.go
// already carries the same (and, for RenderPacstrapExtraConf, a strictly more thorough:
// sorted multi-token dedup, table-driven nil/empty/plain/microarch cases) coverage against
// the same buildkit functions, verified live before deletion.

// TestCachyosRuntimePacmanConf locks in the booted-guest /etc/pacman.conf the
// cachyos pacstrap writes into the rootfs. Single-source guard: runtime_pacman_conf
// is a TEMPLATE that derives its repo list from the one extra_repo source (no
// second hand-maintained copy), so the install + runtime configs cannot drift.
// Regression guard for the "config file /etc/pacman.conf could not be read"
// deploy failure AND for the cachyos-extra HTML-stub repo (must be absent from
// BOTH configs, by construction, now that extra_repo is the single source). This
// test stays in charly — it is a white-box check of charly's OWN production
// box/cachyos distro config (loaded via LoadBuildConfigForBox), not of the pure
// buildkit render functions in isolation (those have their own coverage in
// sdk/buildkit/build_helpers_test.go).
func TestCachyosRuntimePacmanConf(t *testing.T) {
	distroCfg, _, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	cachyos, ok := distroCfg.Distro["cachyos"]
	if !ok || cachyos.Pacstrap == nil {
		t.Fatal("cachyos distro / pacstrap missing from build.yml")
	}
	// Single source: the runtime config must DERIVE its repos from extra_repo
	// (template), not hardcode a second copy.
	if !strings.Contains(cachyos.Pacstrap.RuntimePacmanConf, ".ExtraRepos") {
		t.Errorf("runtime_pacman_conf must derive its repo list from extra_repo via {{ range .ExtraRepos }} (single source), got:\n%s", cachyos.Pacstrap.RuntimePacmanConf)
	}
	// Render it the way the bootstrap paths do.
	rc, err := testRenderRuntimePacmanConf(cachyos.Pacstrap)
	if err != nil {
		t.Fatalf("renderRuntimePacmanConf: %v", err)
	}
	if rc == "" {
		t.Fatal("rendered runtime_pacman_conf is empty — guests boot with no /etc/pacman.conf and add_layer pac installs fail")
	}
	for _, want := range []string{"[options]", "SigLevel = Never", "[cachyos-v3]", "[cachyos-core-v3]", "[cachyos]", "Include = /etc/pacman.d/mirrorlist"} {
		if !strings.Contains(rc, want) {
			t.Errorf("rendered runtime_pacman_conf missing %q:\n%s", want, rc)
		}
	}
	// cachyos-extra serves no usable DB (HTML stub). Removed from the single
	// extra_repo source, it must be absent from BOTH the rendered runtime config
	// AND the install config — the drift this cutover eliminated.
	if strings.Contains(rc, "cachyos-extra") {
		t.Errorf("runtime_pacman_conf must NOT include cachyos-extra:\n%s", rc)
	}
	if strings.Contains(testRenderPacstrapExtraConf(cachyos.Pacstrap), "cachyos-extra") {
		t.Errorf("install (extra_repo) config must NOT include cachyos-extra either — single source of truth")
	}
}
