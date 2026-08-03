package build

import (
	"sort"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/spec/spec"
)

// Relocated from charly/namespace_test.go + charly/resolver_unify_test.go (#55 decoupling cone,
// Batch B, per orchestrator ruling: split by assertion — a test asserting ResolveBox/ResolveAllBox
// OUTPUT is resolver-capability coverage, not charly-loader coverage, even when its fixture is
// shaped by a real import-namespace tree; what the loader PRODUCED (a Config.Namespaces tree) stays
// charly's to test, what ResolveBox MAKES of it is this plugin's). Every fixture below is a literal
// *spec.Config tree (including mutual Namespaces references) rather than a real
// LoadUnified(writeFixture(...)) round trip — Config.Namespaces is a plain map[string]*Config
// (config.go:48), directly constructible, no host/file access needed.

func keysOf(m map[string]*buildkit.ResolvedBox) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// TestResolveBox_NamespacedBaseIsInternal is the moved half of charly's former
// TestResolveImageRef_Qualified: app's base (sub.widget) must be classified INTERNAL
// (IsExternalBase=false) by ResolveBox because it resolves through an import namespace, not
// mistaken for an external OCI URL. The loader-product half (cfg.ResolveBoxRef resolving into the
// right namespace Config) stayed in charly/namespace_test.go.
func TestResolveBox_NamespacedBaseIsInternal(t *testing.T) {
	sub := &spec.Config{
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"widget": {Base: "quay.io/fedora/fedora:43", Build: []string{"rpm"}, Distro: []string{"fedora"}},
		}),
	}
	cfg := &spec.Config{
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"app": {Base: "sub.widget", Build: []string{"rpm"}, Distro: []string{"fedora"}, Candy: []string{}},
		}),
		Namespaces: map[string]*spec.Config{"sub": sub},
	}

	ri, err := buildkit.ResolveBox(cfg, "app", "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveBox(app): %v", err)
	}
	if ri.IsExternalBase {
		t.Error("app.base = sub.widget should be IsExternalBase=false (resolved through namespace)")
	}
}

// TestResolveNamespacedBase_BuilderRefRequalified is the regression guard for the cross-namespace
// builder-ref leak. When the root consumes a namespaced base (`app: base: sub.widget`) whose
// builder map references the base's OWN namespace (`widget: builder: {pixi: up.archlike-builder}`,
// where sub imports root as `up`), pullNamespacedBox must re-qualify that builder ref
// (`up.archlike-builder` -> `sub.up.archlike-builder`) — exactly as it re-qualifies `base:` — so it
// resolves from the root config and matches the key the builder image is pulled under. Mirrors the
// real selkies-labwc (`builder: charly.arch-builder`) consumed by main's android-emulator
// (`base: cachyos.selkies-labwc`).
func TestResolveNamespacedBase_BuilderRefRequalified(t *testing.T) {
	root := &spec.Config{
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"app":              {Base: "sub.widget", Build: []string{"rpm"}, Distro: []string{"fedora"}},
			"archlike-builder": {Base: "quay.io/fedora/fedora:43", Build: []string{"rpm"}, Produce: []string{"pixi"}, Distro: []string{"fedora"}},
		}),
	}
	sub := &spec.Config{
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"buildable": {Plan: []spec.Step{{Run: "install", Op: spec.Op{Command: "true"}}}},
			"widget": {
				Base: "quay.io/fedora/fedora:43", Build: []string{"pac", "aur"},
				Builder: spec.BuilderMap{"pixi": "up.archlike-builder"}, Distro: []string{"fedora"},
				Candy: []string{"buildable"},
			},
		}),
	}
	root.Namespaces = map[string]*spec.Config{"sub": sub}
	sub.Namespaces = map[string]*spec.Config{"up": root} // the mutual up<->sub cycle

	resolved, err := buildkit.ResolveAllBox(root, "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveAllBox must NOT fail when a namespaced base's builder ref points into the base's own namespace: %v", err)
	}
	w, ok := resolved["sub.widget"]
	if !ok {
		t.Fatal("sub.widget not pulled into the resolved set")
	}
	if got := w.Builder.BuilderFor("pixi"); got != "sub.up.archlike-builder" {
		t.Errorf("widget builder ref not re-qualified: got %q, want %q", got, "sub.up.archlike-builder")
	}
	if _, ok := resolved["sub.up.archlike-builder"]; !ok {
		t.Errorf("re-qualified builder image sub.up.archlike-builder absent from resolved set (keys: %v)", keysOf(resolved))
	}
}

// TestResolveBuilder_DistroKeyed_NoExplicitMap is the regression guard for the distro-keyed builder
// default: an image whose base is reached through an import namespace and resolves to a
// cachyos/Arch distro must auto-select arch-builder WITHOUT any per-image `builder:` declaration —
// the root `arch` image (whose distro: matches and whose bare arch-builder ref resolves in root)
// supplies it. Without the fix this resolves fedora-builder (the Fedora-only defaults.builder) —
// the exact bug that silently built a Fedora builder for cachyos images.
func TestResolveBuilder_DistroKeyed_NoExplicitMap(t *testing.T) {
	root := &spec.Config{
		Defaults: spec.BoxConfig{Builder: spec.BuilderMap{"pixi": "fedora-builder", "npm": "fedora-builder"}},
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"arch": {
				Base: "quay.io/cachyos/cachyos:latest", Build: []string{"pac"},
				Builder: spec.BuilderMap{"pixi": "arch-builder", "npm": "arch-builder"}, Distro: []string{"arch"},
			},
			"arch-builder":   {Base: "quay.io/cachyos/cachyos:latest", Build: []string{"pac"}, Produce: []string{"pixi", "npm"}, Distro: []string{"arch"}},
			"fedora-builder": {Base: "quay.io/fedora/fedora:43", Build: []string{"rpm"}, Produce: []string{"pixi", "npm"}, Distro: []string{"fedora"}},
			"cachyos-app":    {Base: "sub.cachyos"},
			"fedora-app":     {Base: "sub.fedora"},
		}),
	}
	sub := &spec.Config{
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"cachyos": {Base: "quay.io/cachyos/cachyos:latest", Build: []string{"pac", "aur"}, Distro: []string{"cachyos", "arch"}},
			"fedora":  {Base: "quay.io/fedora/fedora:43", Build: []string{"rpm"}, Distro: []string{"fedora"}},
		}),
	}
	root.Namespaces = map[string]*spec.Config{"sub": sub}
	sub.Namespaces = map[string]*spec.Config{"up": root}

	resolved, err := buildkit.ResolveAllBox(root, "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveAllBox: %v", err)
	}
	app, ok := resolved["cachyos-app"]
	if !ok {
		t.Fatalf("cachyos-app not resolved (keys: %v)", keysOf(resolved))
	}
	// THE FIX: namespaced cachyos/arch base → arch-builder, no per-image map.
	if got := app.Builder.BuilderFor("pixi"); got != "arch-builder" {
		t.Errorf("cachyos-app pixi builder = %q, want arch-builder (distro-keyed default)", got)
	}
	if got := app.Builder.BuilderFor("npm"); got != "arch-builder" {
		t.Errorf("cachyos-app npm builder = %q, want arch-builder", got)
	}
	// Guard: a fedora-distro image must still resolve fedora-builder.
	fa, ok := resolved["fedora-app"]
	if !ok {
		t.Fatalf("fedora-app not resolved")
	}
	if got := fa.Builder.BuilderFor("pixi"); got != "fedora-builder" {
		t.Errorf("fedora-app pixi builder = %q, want fedora-builder (no regression)", got)
	}
}

// literalUnreachableNamespaceFixture is the plugin-side literal reconstruction of charly's former
// resolver_unify_test.go fixtureNamespacedProject: `app` (root, external fedora base) and
// `sub.widget` (namespaced, external fedora base); `app` does NOT base off `sub.widget`, so
// `sub.widget` is NOT reachable as a base — exercising the explicit-target and direct-resolve paths
// rather than the base-reachability pull.
func literalUnreachableNamespaceFixture() *spec.Config {
	sub := &spec.Config{
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"widget": {Base: "quay.io/fedora/fedora:43", Build: []string{"rpm"}, Distro: []string{"fedora"}, Candy: []string{}},
		}),
	}
	return &spec.Config{
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"app": {Base: "quay.io/fedora/fedora:43", Build: []string{"rpm"}, Distro: []string{"fedora"}, Candy: []string{}},
		}),
		Namespaces: map[string]*spec.Config{"sub": sub},
	}
}

// TestResolveImage_QualifiedDelegates is the central-chokepoint guard: ResolveBox must resolve a
// namespace-qualified name by delegating into the owning namespace Config. Pre-fix,
// `c.Box["sub.widget"]` missed and this returned "image \"sub.widget\" not found".
func TestResolveImage_QualifiedDelegates(t *testing.T) {
	cfg := literalUnreachableNamespaceFixture()

	ri, err := buildkit.ResolveBox(cfg, "sub.widget", "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveBox(\"sub.widget\") must resolve via namespace delegation: %v", err)
	}
	if ri.Name != "widget" {
		t.Errorf("resolved name = %q, want %q (leaf, resolved in the namespace context)", ri.Name, "widget")
	}
	if ri.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("resolved base = %q, want the namespace image's base", ri.Base)
	}

	// Bare names still resolve in root, unchanged.
	if _, err := buildkit.ResolveBox(cfg, "app", "test", "", resolveOptsFixture(spec.ResolveOpts{})); err != nil {
		t.Errorf("bare ResolveBox(\"app\") regressed: %v", err)
	}
	// A genuinely-missing namespace still errors clearly.
	if _, err := buildkit.ResolveBox(cfg, "nope.widget", "test", "", resolveOptsFixture(spec.ResolveOpts{})); err == nil {
		t.Error("ResolveBox(\"nope.widget\") should error: no such namespace")
	}
}

// TestResolveAllImage_RequestedQualifiedTarget guards the build-target path: an
// explicitly-requested qualified box that is NOT a base/builder of any root box must still land in
// the resolved set (so filterBox / the build graph accept `charly box build sub.widget` and the
// ensure-image build-fallback for a namespaced builder). Pre-fix it was absent.
func TestResolveAllImage_RequestedQualifiedTarget(t *testing.T) {
	cfg := literalUnreachableNamespaceFixture()

	// Without RequestedBoxes, sub.widget is not reachable, so not pulled.
	base, err := buildkit.ResolveAllBox(cfg, "test", "", resolveOptsFixture(spec.ResolveOpts{}))
	if err != nil {
		t.Fatalf("ResolveAllBox: %v", err)
	}
	if _, present := base["sub.widget"]; present {
		t.Fatal("sub.widget should NOT be in the resolved set without an explicit request (it is not a base of any root image)")
	}

	// With it requested, it is pulled under its fully-qualified key.
	withReq, err := buildkit.ResolveAllBox(cfg, "test", "", resolveOptsFixture(spec.ResolveOpts{RequestedBoxes: []string{"sub.widget"}}))
	if err != nil {
		t.Fatalf("ResolveAllBox(RequestedBoxes): %v", err)
	}
	if _, present := withReq["sub.widget"]; !present {
		t.Errorf("requested qualified target sub.widget absent from resolved set (keys: %v)", keysOf(withReq))
	}
}
