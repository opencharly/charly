package fleet

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// builder_scripts_test.go — relocated from charly/builder_scripts_test.go (#55 decoupling,
// Batch A): all 5 tests assert deploykit.RenderBuilderScript output text directly. Charly's
// original builderStepWithDef loaded the REAL build vocabulary via charly's own
// LoadBuildConfigForBox (the project loader) purely to source a realistic *spec.Builder
// fixture — unavailable to this out-of-module plugin package. testBuilderDef below is a
// literal fixture reproducing the embedded charly/charly.yml `builder:` section's
// pixi/npm/cargo/aur entries VERBATIM (the exact phase.install.host bash cells these tests
// assert against), so the assertions exercise the SAME real template text without going
// through the loader.

// testBuilderDef returns the *spec.Builder for name, reproducing charly/charly.yml's
// embedded `builder:` section entry verbatim.
func testBuilderDef(name string) *spec.Builder {
	switch name {
	case "pixi":
		return &spec.Builder{
			DetectFiles: []string{"pixi.toml", "pyproject.toml", "environment.yml"},
			Phases: &spec.PhaseSet{Install: &spec.PhaseTemplates{Host: `set -e
cd "$HOME"
if [ -f /work/pixi.toml ]; then manifest=pixi.toml
elif [ -f /work/pyproject.toml ]; then manifest=pyproject.toml
elif [ -f /work/environment.yml ]; then manifest=environment.yml
else echo 'no pixi manifest found in /work' >&2; exit 1; fi
cp /work/$manifest $manifest
if [ -f /work/pixi.lock ]; then cp /work/pixi.lock pixi.lock; fi
# Ensure manylinux glibc requirement is present so cross-distro compat holds.
grep -q 'system-requirements' $manifest || printf '\n[system-requirements]\nlibc = { family = "glibc", version = "2.39" }\n' >> $manifest
case "$manifest" in
  pixi.toml)
    if [ -f pixi.lock ]; then pixi install --frozen
    else pixi install; fi ;;
  pyproject.toml) pixi install --manifest-path pyproject.toml ;;
  environment.yml) pixi project import environment.yml && pixi install ;;
esac
if [ -f /work/build.sh ]; then bash /work/build.sh; fi
rm -f $manifest pixi.lock
`}},
		}
	case "npm":
		return &spec.Builder{
			DetectFiles: []string{"package.json"},
			Phases: &spec.PhaseSet{Install: &spec.PhaseTemplates{Host: `set -e
if [ ! -f /work/package.json ]; then echo 'no package.json in /work' >&2; exit 1; fi
STAGE=$(mktemp -d)
cp /work/package.json "$STAGE/package.json"
cd "$STAGE"
node -e 'var d=require("./package.json").dependencies||{};for(var[n,v]of Object.entries(d))console.log(v==="*"?n:n+"@"+v)' | xargs -r npm install -g
rm -rf "$STAGE"
`}},
		}
	case "cargo":
		return &spec.Builder{
			DetectFiles:    []string{"Cargo.toml"},
			RequiresSrcDir: true,
			Inline:         true,
			Phases: &spec.PhaseSet{Install: &spec.PhaseTemplates{Host: `set -e
if [ ! -f /work/Cargo.toml ]; then echo 'no Cargo.toml in /work' >&2; exit 1; fi
cargo install --path /work --root "$CARGO_HOME"
`}},
		}
	case "aur":
		return &spec.Builder{
			DetectConfig: "aur",
			Phases: &spec.PhaseSet{Install: &spec.PhaseTemplates{Host: `set -e
echo '%wheel ALL=(ALL:ALL) NOPASSWD: ALL' > /etc/sudoers.d/20-nopasswd-wheel
chmod 0440 /etc/sudoers.d/20-nopasswd-wheel
getent group wheel >/dev/null || groupadd wheel
usermod -aG wheel user
id -nG user | tr ' ' '\n' | grep -qx wheel || { echo 'FATAL: user not in wheel group' >&2; exit 1; }
mkdir -p /tmp/aur-build
chown -R user:user /tmp/aur-build
cp /etc/makepkg.conf /tmp/makepkg.conf
sed -i '/^OPTIONS/s/ debug/ !debug/' /tmp/makepkg.conf
chown user:user /tmp/makepkg.conf
pacman -Syu --noconfirm
sudo -u user -- yay -S --noconfirm --needed --builddir /tmp/aur-build --makepkgconf /tmp/makepkg.conf{{range .Packages}} {{shquote .}}{{end}}
mkdir -p /tmp/aur-pkgs
for src in /tmp/aur-build /home/user/.cache/yay /root/.cache/yay; do
  [ -d "$src" ] && find "$src" -name '*.pkg.tar.zst' -exec cp {} /tmp/aur-pkgs/ \; 2>/dev/null || true
done
echo 'aur artifacts staged for host install:' >&2
ls -la /tmp/aur-pkgs/ >&2
`}},
		}
	default:
		return nil
	}
}

// builderStepWithDef returns a BuilderStep carrying testBuilderDef(name), so
// deploykit.RenderBuilderScript renders the actual phase.install.host cell.
func builderStepWithDef(t *testing.T, name string, raw map[string]any) *spec.BuilderStep {
	t.Helper()
	bDef := testBuilderDef(name)
	if bDef == nil {
		t.Fatalf("builder %q not defined in the test fixture", name)
	}
	return &spec.BuilderStep{Builder: name, CandyName: "test-layer", BuilderDef: bDef, RawStageContext: raw}
}

func TestRenderPixiScript(t *testing.T) {
	s := builderStepWithDef(t, "pixi", nil)
	out, err := deploykit.RenderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	mustContain := []string{
		"set -e",
		`cd "$HOME"`,
		"pixi install",
		"system-requirements",
		`cp /work/$manifest $manifest`,
	}
	for _, m := range mustContain {
		if !strings.Contains(out, m) {
			t.Errorf("missing %q in pixi script:\n%s", m, out)
		}
	}
}

func TestRenderNpmScript(t *testing.T) {
	s := builderStepWithDef(t, "npm", nil)
	out, err := deploykit.RenderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "npm install -g") {
		t.Errorf("missing npm install -g: %s", out)
	}
	if !strings.Contains(out, "package.json") {
		t.Errorf("missing package.json handling: %s", out)
	}
}

func TestRenderCargoScript(t *testing.T) {
	s := builderStepWithDef(t, "cargo", nil)
	out, err := deploykit.RenderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, `cargo install --path /work --root "$CARGO_HOME"`) {
		t.Errorf("missing cargo install line: %s", out)
	}
}

func TestRenderAurScriptPackages(t *testing.T) {
	s := builderStepWithDef(t, "aur", map[string]any{
		"packages": []string{"some-pkg", "another-pkg"},
	})
	out, err := deploykit.RenderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	mustContain := []string{
		"yay -S --noconfirm --needed",
		"some-pkg",
		"another-pkg",
		"/tmp/aur-pkgs",
		"*.pkg.tar.zst",
		// The DB refresh that keeps the (cached, stale) builder DB from
		// resolving a makedepend to a mirror-rotated version (the go-1.26.3
		// .sig 404). Mirrors the aur builder's OpResolve stage (kit.BuilderResolve, R3).
		"pacman -Syu --noconfirm",
	}
	for _, m := range mustContain {
		if !strings.Contains(out, m) {
			t.Errorf("aur script missing %q:\n%s", m, out)
		}
	}
	// The refresh MUST precede yay's makedepend resolution, or it's useless.
	if syncIdx, yayIdx := strings.Index(out, "pacman -Syu --noconfirm"), strings.Index(out, "yay -S"); syncIdx < 0 || yayIdx < 0 || syncIdx > yayIdx {
		t.Errorf("pacman -Syu must come BEFORE yay -S (sync=%d yay=%d):\n%s", syncIdx, yayIdx, out)
	}
}

func TestRenderBuilderScriptUnknownBuilder(t *testing.T) {
	// A BuilderStep with no resolved *spec.Builder (synthetic / unknown builder)
	// has no host cell to render → error.
	s := &spec.BuilderStep{Builder: "nonexistent"}
	if _, err := deploykit.RenderBuilderScript(s, "/home/user"); err == nil {
		t.Fatalf("expected error for builder with no *spec.Builder")
	}
}
