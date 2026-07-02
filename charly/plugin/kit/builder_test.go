package kit

import (
	"reflect"
	"testing"

	"github.com/opencharly/charly/charly/spec"
)

// TestBuilderCollectContext covers the per-builder deploy-time stage-context derivation that the
// four externalized detection-builder plugins (cargo/npm/pixi/aur) serve via OpCollectContext.
func TestBuilderCollectContext(t *testing.T) {
	cases := []struct {
		word string
		in   spec.BuilderCollectInput
		want map[string]any
	}{
		{"pixi", spec.BuilderCollectInput{Candy: "jupyter"}, map[string]any{"env_name": "default"}},
		{"npm", spec.BuilderCollectInput{Candy: "claude-code"}, nil},
		{"cargo", spec.BuilderCollectInput{Candy: "ripgrep"}, nil},
		{
			"aur",
			spec.BuilderCollectInput{Candy: "chrome", Packages: []string{"google-chrome"}, Replaces: []string{"chromium"}},
			map[string]any{"packages": []string{"google-chrome"}, "replaces": []string{"chromium"}},
		},
		{"aur", spec.BuilderCollectInput{Candy: "x"}, map[string]any{}},
		{"unknown", spec.BuilderCollectInput{Candy: "x"}, nil},
	}
	for _, tc := range cases {
		got := BuilderCollectContext(tc.word, tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("BuilderCollectContext(%q) = %+v, want %+v", tc.word, got, tc.want)
		}
	}
}

// TestBuilderReverse covers the per-builder teardown-op KIND derivation the four plugins serve via
// OpReverse — the logic this externalization moves out-of-process. Context arrives JSON-decoded
// (string slices as []any), so the []any decode path is exercised too.
func TestBuilderReverse(t *testing.T) {
	// pixi → pixi-env-remove (user scope, Extra[layer]=candy).
	ops := BuilderReverse("pixi", spec.BuilderReverseInput{Candy: "jupyter", Context: map[string]any{"env_name": "default"}})
	if len(ops) != 1 || ops[0].Kind != spec.ReverseOpPixiEnvRemove || ops[0].Scope != spec.ScopeUser {
		t.Fatalf("pixi reverse = %+v, want [pixi-env-remove user]", ops)
	}
	if ops[0].Extra["layer"] != "jupyter" || len(ops[0].Targets) != 1 || ops[0].Targets[0] != "default" {
		t.Fatalf("pixi reverse op fields = %+v", ops[0])
	}

	// aur → package-remove (system scope, pac format); []any decode path.
	ops = BuilderReverse("aur", spec.BuilderReverseInput{Candy: "chrome", Context: map[string]any{"packages": []any{"google-chrome"}}})
	if len(ops) != 1 || ops[0].Kind != spec.ReverseOpPackageRemove || ops[0].Scope != spec.ScopeSystem || ops[0].Format != "pac" {
		t.Fatalf("aur reverse = %+v, want [package-remove system pac]", ops)
	}

	// npm → npm-uninstall-g when packages present; nil when absent (the best-effort no-op).
	ops = BuilderReverse("npm", spec.BuilderReverseInput{Context: map[string]any{"packages": []string{"@anthropic-ai/claude-code"}}})
	if len(ops) != 1 || ops[0].Kind != spec.ReverseOpNpmUninstallG {
		t.Fatalf("npm reverse = %+v, want [npm-uninstall-g]", ops)
	}
	if got := BuilderReverse("npm", spec.BuilderReverseInput{}); got != nil {
		t.Fatalf("npm reverse with empty context = %+v, want nil", got)
	}

	// cargo → cargo-uninstall when binaries present; nil otherwise.
	ops = BuilderReverse("cargo", spec.BuilderReverseInput{Context: map[string]any{"binaries": []string{"rg", "fd"}}})
	if len(ops) != 1 || ops[0].Kind != spec.ReverseOpCargoUninstall || len(ops[0].Targets) != 2 {
		t.Fatalf("cargo reverse = %+v, want [cargo-uninstall rg fd]", ops)
	}

	// unknown / empty → no teardown.
	if got := BuilderReverse("unknown", spec.BuilderReverseInput{}); got != nil {
		t.Fatalf("unknown reverse = %+v, want nil", got)
	}
}
