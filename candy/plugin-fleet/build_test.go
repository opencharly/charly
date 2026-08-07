package fleet

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// build_test.go — relocated (in part) from charly/build_test.go (#55 decoupling, Batch A):
// the FilterBox portion — these 4 tests assert deploykit.FilterBox directly, zero charly
// coupling. TestHostPlatform/TestRenderPacstrapExtraConf/TestCachyosRuntimePacmanConf (the
// buildkit-resolve portion) are a SEPARATE batch's concern (plugin-build) and stay in charly's
// build_test.go untouched by this batch.

func TestFilterImages(t *testing.T) {
	images := map[string]*buildkit.ResolvedBox{"fedora": {ResolvedBox: spec.ResolvedBox{Name: "fedora", IsExternalBase: true}}, "fedora-test": {ResolvedBox: spec.ResolvedBox{Name: "fedora-test", Base: "fedora", IsExternalBase: false}}, "ubuntu": {ResolvedBox: spec.ResolvedBox{Name: "ubuntu", IsExternalBase: true}}}

	order := []string{"fedora", "ubuntu", "fedora-test"}

	// Request only fedora-test — should pull in fedora as dependency
	filtered, err := deploykit.FilterBox(order, []string{"fedora-test"}, images)
	if err != nil {
		t.Fatalf("deploykit.FilterBox() error: %v", err)
	}
	want := []string{"fedora", "fedora-test"}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("deploykit.FilterBox() = %v, want %v", filtered, want)
	}
}

func TestFilterImagesUnknown(t *testing.T) {
	images := map[string]*buildkit.ResolvedBox{"fedora": {ResolvedBox: spec.ResolvedBox{Name: "fedora", IsExternalBase: true}}}
	_, err := deploykit.FilterBox([]string{"fedora"}, []string{"nonexistent"}, images)
	if err == nil {
		t.Error("expected error for unknown image")
	}
}

func TestFilterImagesIncludesBuilder(t *testing.T) {
	images := map[string]*buildkit.ResolvedBox{"builder": {ResolvedBox: spec.ResolvedBox{Name: "builder", IsExternalBase: true}}, "fedora": {ResolvedBox: spec.ResolvedBox{Name: "fedora", IsExternalBase: true, Builder: spec.BuilderMap{"pixi": "builder", "npm": "builder"}}}, "app": {ResolvedBox: spec.ResolvedBox{Name: "app", Base: "fedora", IsExternalBase: false, Builder: spec.BuilderMap{"pixi": "builder", "npm": "builder"}}}}

	order := []string{"builder", "fedora", "app"}

	// Request only app — should pull in fedora (base) and builder
	filtered, err := deploykit.FilterBox(order, []string{"app"}, images)
	if err != nil {
		t.Fatalf("deploykit.FilterBox() error: %v", err)
	}
	want := []string{"builder", "fedora", "app"}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("deploykit.FilterBox() = %v, want %v", filtered, want)
	}
}

func TestFilterImagesIncludesBootstrapBuilder(t *testing.T) {
	// Regression: 2026-05 cachyos / cachyos-pacstrap-builder bug. Requesting
	// the downstream `app` (base: cachyos) must pull cachyos-pacstrap-builder
	// into the filtered set even though it's referenced via the dedicated
	// BootstrapBuilderImage field, not via Builder map. Without this, the
	// `charly update --build versa` path silently skipped scheduling
	// cachyos-pacstrap-builder, and runPrivilegedBootstrap then hard-failed
	// at resolveLocalImageRef with "build the bootstrap_builder_image first".
	images := map[string]*buildkit.ResolvedBox{"arch": {ResolvedBox: spec.ResolvedBox{Name: "arch", IsExternalBase: true}}, "cachyos-pacstrap-builder": {ResolvedBox: spec.ResolvedBox{Name: "cachyos-pacstrap-builder", Base: "arch", IsExternalBase: false}}, "cachyos": {ResolvedBox: spec.ResolvedBox{Name: "cachyos", IsExternalBase: true, BootstrapBuilderImage: "cachyos-pacstrap-builder"}}, "app": {ResolvedBox: spec.ResolvedBox{Name: "app", Base: "cachyos", IsExternalBase: false}}}

	order := []string{"arch", "cachyos-pacstrap-builder", "cachyos", "app"}

	filtered, err := deploykit.FilterBox(order, []string{"app"}, images)
	if err != nil {
		t.Fatalf("deploykit.FilterBox() error: %v", err)
	}
	want := []string{"arch", "cachyos-pacstrap-builder", "cachyos", "app"}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("deploykit.FilterBox() = %v, want %v", filtered, want)
	}
}
