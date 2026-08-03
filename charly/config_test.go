package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

func TestLoadConfig(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Check defaults
	if cfg.Defaults.Registry != "ghcr.io/test" {
		t.Errorf("Defaults.Registry = %q, want %q", cfg.Defaults.Registry, "ghcr.io/test")
	}
	if len(cfg.Defaults.Build) != 1 || cfg.Defaults.Build[0] != "rpm" {
		t.Errorf("Defaults.Build = %v, want [rpm]", cfg.Defaults.Build)
	}

	// Check images exist
	expectedImages := []string{"base", "cuda", "ml-cuda", "inference", "ubuntu-dev", "bazzite"}
	for _, name := range expectedImages {
		if _, ok := cfg.Box[name]; !ok {
			t.Errorf("missing image %q", name)
		}
	}
}

// TestMergeBoxConfig_BuildTunables relocated to sdk/loaderkit/merge_test.go
// alongside mergeBoxConfig (K1-proper — the merge half of the loader moved to loaderkit).

func TestImageNames(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	names := cfg.BoxNames()
	// 7 total images in testdata, but disabled-image is excluded
	if len(names) != 6 {
		t.Errorf("BoxNames() returned %d names, want 6: %v", len(names), names)
	}

	// Should be sorted
	for i := 0; i < len(names)-1; i++ {
		if names[i] > names[i+1] {
			t.Errorf("BoxNames() not sorted: %v", names)
			break
		}
	}

	// disabled-image should not appear
	for _, name := range names {
		if name == "disabled-image" {
			t.Error("BoxNames() should not include disabled-image")
		}
	}
}

// TestResolveImage / TestResolveImageNotFound / TestResolveImageBuilders / TestCollectBoxPorts /
// TestFullTag / TestEnabledField / TestResolveImageDistroBaseChain / TestResolveImageBuildBaseChain
// moved to candy/plugin-build/box_resolve_test.go (#55 decoupling cone, Batch B) — each asserts
// buildkit.ResolveBox / ResolveAllBox / deploykit.CollectBoxPorts directly, zero charly coupling.

// TestResolveOpts_ShouldIncludeDisabled covers the scoping helper used by
// ResolveBox / ResolveAllBox / validateBoxDAG. The scope semantics
// matter for `charly box build <name> --include-disabled` so widening the
// working set doesn't surface unrelated disabled-image dep errors.
func TestResolveOpts_ShouldIncludeDisabled(t *testing.T) {
	cases := []struct {
		name string
		opts spec.ResolveOpts
		want map[string]bool // image-name → expected return
	}{
		{
			name: "default opts: never include",
			opts: spec.ResolveOpts{},
			want: map[string]bool{"foo": false, "bar": false},
		},
		{
			name: "global IncludeDisabled: include all",
			opts: spec.ResolveOpts{IncludeDisabled: true},
			want: map[string]bool{"foo": true, "bar": true},
		},
		{
			name: "scoped IncludeDisabled: only listed names",
			opts: spec.ResolveOpts{
				IncludeDisabled:      true,
				IncludeDisabledNames: map[string]bool{"foo": true},
			},
			want: map[string]bool{"foo": true, "bar": false},
		},
		{
			name: "scoped without IncludeDisabled flag: never include (flag is the gate)",
			opts: spec.ResolveOpts{
				IncludeDisabledNames: map[string]bool{"foo": true},
			},
			want: map[string]bool{"foo": false, "bar": false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for image, want := range tc.want {
				if got := tc.opts.ShouldIncludeDisabled(image); got != want {
					t.Errorf("shouldIncludeDisabled(%q) = %v, want %v", image, got, want)
				}
			}
		})
	}
}
