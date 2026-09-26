package main

import (
	"os"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// TestUninstallTemplatesStripVersionPin guards the uninstall side of the package
// version pin (RCA 2026-09-25). A candy may pin an exact version in
// `distro: <d>. package:` (`name=VERSION` on pac/apt/apk, `name-VERSION-<rel>`
// on rpm) so the install RUN text changes on a version bump and invalidates
// exactly the install layer. But the SAME `.Packages` list feeds each format's
// `uninstall_template`, and pacman/apk REJECT a versioned spec for removal
// (`pacman -Rs charly=2026.268.1917` -> "target not found"). Each uninstall
// template therefore renders `{{pkgName .}}` to strip the pin back to the bare
// name. This test FAILS without that strip.
//
// It lives HERE (the repo that owns the templates) and renders the discovered
// template with the stdlib engine over a local `pkgNameForTest` mirror — charly
// core is FORBIDDEN by the import-purity gate (charly/import_purity_test.go)
// from importing the sdk MECHANISM KITS, so the shared helper is not reachable
// from here. The PRODUCTION render path is proven elsewhere and independently:
//   - `pkgName` is REGISTERED in the runtime `TemplateFuncs` of
//     `sdk/buildkit/render.go` (sdk `origin/main` via opencharly/sdk#284).
//   - the uninstall command is rendered by that exact map: candy/plugin-fleet's
//     `recordDeploy` calls `buildkit.RenderTemplate(format+"-uninstall",
//     fd.UninstallTemplate, ictx)` through `kit.FillReverseUninstallCmds`
//     (plugin-fleet/candy/plugin-fleet/deploy_target.go), and
//     `TestExternalDeploy_FillsPackageRemoveUninstallCmdOnRecord` +
//     `TestPinnedInstallRendersPinAndBareUninstall` (sdk) exercise it with a
//     pinned spec.
//
// This test guards the template STRINGS the repo owns; the production render is
// guarded by those two tests.
func TestUninstallTemplatesStripVersionPin(t *testing.T) {
	funcs := template.FuncMap{
		"pkgName": pkgNameForTest,
	}
	cases := []struct {
		format  string
		pinned  string
		wantSub string // the rendered uninstall must contain this bare form
	}{
		{"apk", "charly=2026.268.1917", "apk del charly"},
		{"pac", "charly=2026.268.1917", "pacman -Rs --noconfirm charly"},
		{"deb", "charly=2026.268.1917", "apt-get purge -y charly"},
		{"rpm", "charly-2026.268.1917-1", "dnf remove -y charly"},
	}

	raw, err := os.ReadFile("charly.yml")
	if err != nil {
		t.Fatalf("read charly.yml: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse charly.yml: %v", err)
	}
	tmpls := map[string]string{}
	collectUninstallTemplates(doc, tmpls)

	for _, tc := range cases {
		tmplStr, ok := tmpls[tc.format]
		if !ok || strings.TrimSpace(tmplStr) == "" {
			t.Errorf("format %q: no uninstall_template found in charly.yml (found: %v)", tc.format, formatKeysOf(tmpls))
			continue
		}
		tm, perr := template.New(tc.format).Funcs(funcs).Parse(tmplStr)
		if perr != nil {
			t.Errorf("format %q: parse: %v", tc.format, perr)
			continue
		}
		var sb strings.Builder
		if eerr := tm.Execute(&sb, map[string]any{"Packages": []string{tc.pinned}}); eerr != nil {
			t.Errorf("format %q: execute: %v", tc.format, eerr)
			continue
		}
		out := sb.String()
		if !strings.Contains(out, tc.wantSub) {
			t.Errorf("format %q: uninstall = %q, want it to contain %q", tc.format, out, tc.wantSub)
		}
		if strings.Contains(out, tc.pinned) {
			t.Errorf("format %q: uninstall = %q still carries the pinned spec %q", tc.format, out, tc.pinned)
		}
	}
}

// collectUninstallTemplates records every `uninstall_template` string keyed by
// the map KEY that held it (the format name, e.g. `pac`/`deb`/`rpm`/`apk`).
func collectUninstallTemplates(node any, out map[string]string) {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			cm, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if ut, ok := cm["uninstall_template"].(string); ok {
				out[key] = ut
				continue
			}
			collectUninstallTemplates(cm, out)
		}
	case []any:
		for _, child := range v {
			collectUninstallTemplates(child, out)
		}
	}
}

func formatKeysOf(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// pkgNameForTest mirrors sdk/buildkit's `pkgName` template func EXACTLY (the
// charly module must not import sdk, so the shared helper is not reachable): a
// pinned spec (`name=VERSION` on pac/apt/apk, `name-<version>-<rel>` on rpm) is
// stripped back to the bare package NAME. Only a suffix that starts with a digit
// and contains a dot is stripped, so a genuinely hyphenated name such as
// `libfoo-2` or `gtk-3` is left intact.
func pkgNameForTest(s string) string {
	if i := strings.IndexByte(s, '='); i >= 0 {
		return s[:i]
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '-' || i+1 >= len(s) || s[i+1] < '0' || s[i+1] > '9' {
			continue
		}
		if strings.ContainsRune(s[i+1:], '.') {
			return s[:i]
		}
	}
	return s
}
