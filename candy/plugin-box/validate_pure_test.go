package box

// validate_pure_test.go — envelope-unit tests for two former charly-core validate tests (task #60) that
// a fixture through `charly box validate` cannot faithfully re-express:
//
//   - TestLevenshteinDistance: a PURE helper. The charly-core copy is deleted with the validate engine;
//     plugin-box owns the surviving copy, so its unit test lives here (package box).
//   - TestValidatePkgConfig_ModulesRequirePackages: the "distro.<name>.modules requires packages" rule.
//     A real authored `distro.<name>.module:` produces the TagSection Raw KEY "module" (singular — see
//     derivePackageSectionsFromCalamares in charly/layers.go), but validatePkgConfig checks Raw["modules"]
//     (plural), so no LOADABLE fixture ever reaches the rule. The former synthetic host test injected a
//     Raw["modules"] section directly; this envelope-unit reproduces that exact injection over the
//     resolved-project envelope so the rule's logic keeps coverage. (The module/modules key mismatch is
//     flagged in the task report — it is pre-existing behaviour in the moved code, not introduced here.)

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestLevenshteinDistance ← charly/validate_test.go TestLevenshteinDistance (the host copy is deleted
// with the engine; plugin-box owns the surviving pure helper).
func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"pixi", "pixi", 0},
		{"pixi", "pixie", 1},
		{"pixi", "pxi", 1},
		{"pixi", "python", 5},
	}
	for _, tt := range tests {
		if got := levenshteinDistance(tt.a, tt.b); got != tt.want {
			t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestValidatePkgConfig_ModulesRequirePackages ← charly/validate_test.go TestValidateModulesWithoutPackages.
// Envelope unit: a candy carrying a format-section Raw["modules"] entry and no packages anywhere must
// be flagged. Not expressible as a loadable fixture (a real `module:` produces Raw key "module").
func TestValidatePkgConfig_ModulesRequirePackages(t *testing.T) {
	rp := &spec.ResolvedProject{
		CandyModels: map[string]spec.CandyModel{
			"mylyr": {
				Name: "mylyr",
				FormatSections: map[string]spec.PackageSection{
					"rpm": {FormatName: "rpm", Raw: map[string]any{"modules": []any{"valkey:remi-9.0"}}},
				},
			},
		},
		Candies: map[string]spec.CandyView{"mylyr": {}},
	}
	vc := newVctx(rp)
	e := &vErr{}
	validatePkgConfig(vc, e)
	got := strings.Join(e.msgs, "\n")
	if !strings.Contains(got, "rpm.modules requires packages") {
		t.Fatalf("want 'rpm.modules requires packages', got: %s", got)
	}
}

// TestCandyHasOrphanPackaged ← charly/validate_packaged_services_test.go (moved with the engine; the
// helper reads spec.CandyModel.Service — the preserve_user-warning suppression: a use_packaged service
// with no same-name custom-exec sibling is a genuine supervisord-drop orphan). Envelope-unit: it tests
// a pure predicate over the build model, which no error-severity fixture verdict can express (the
// finding is a WARNING filtered from the verdict). The old "nil layer" case → the empty CandyModel{}.
func TestCandyHasOrphanPackaged(t *testing.T) {
	tests := []struct {
		name  string
		model spec.CandyModel
		want  bool
	}{
		{"no services", spec.CandyModel{}, false},
		{"mixed-form (packaged + same-name exec sibling) — sshd — no orphan", spec.CandyModel{Service: []spec.CandyService{{Name: "sshd", UsePackaged: "sshd.service"}, {Name: "sshd", Exec: "/usr/local/bin/sshd-wrapper"}}}, false},
		{"packaged-only — postgresql — orphan", spec.CandyModel{Service: []spec.CandyService{{Name: "postgresql", UsePackaged: "postgresql.service"}}}, true},
		{"packaged with a DIFFERENT-name exec sibling — still orphan", spec.CandyModel{Service: []spec.CandyService{{Name: "postgresql", UsePackaged: "postgresql.service"}, {Name: "other", Exec: "/bin/other"}}}, true},
		{"custom-only — no orphan", spec.CandyModel{Service: []spec.CandyService{{Name: "svc", Exec: "svc serve"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := candyHasOrphanPackaged(tt.model); got != tt.want {
				t.Errorf("candyHasOrphanPackaged() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValidateCandyApk ← charly/android_spec_test.go (moved with the engine). The apk⊕source cross-field
// rule (source: applies only to package installs, never a committed apk:) is the one Go rule left after
// #CandyApk took the package⊕apk one-of + source enum. Envelope-unit: a 1:1 helper port (the old test
// called validateCandyApk(name, apks, errs) directly; the plugin owns it over *vErr).
func TestValidateCandyApk(t *testing.T) {
	cases := []struct {
		name    string
		apks    []spec.ApkPackageSpec
		wantErr bool
	}{
		{"valid-package", []spec.ApkPackageSpec{{Package: "org.fdroid.fdroid", Source: "apk-pure"}}, false},
		{"valid-committed", []spec.ApkPackageSpec{{Apk: "tests/data/x.apk"}}, false},
		{"source-on-committed", []spec.ApkPackageSpec{{Apk: "y.apk", Source: "apk-pure"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &vErr{}
			validateCandyApk("test-layer", tc.apks, e)
			if (len(e.msgs) > 0) != tc.wantErr {
				t.Errorf("validateCandyApk(%+v): hasErr=%v want %v (%v)", tc.apks, len(e.msgs) > 0, tc.wantErr, e.msgs)
			}
		})
	}
}
