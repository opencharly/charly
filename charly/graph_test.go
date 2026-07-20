package main

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"
)

func TestResolveCandyOrder(t *testing.T) {
	// Create test candies
	layers := map[string]spec.CandyReader{
		"pixi":    testCandy("pixi", spec.CandyModel{}, spec.CandyView{}),
		"python":  testCandy("python", spec.CandyModel{}, spec.CandyView{Require: []string{"pixi"}}),
		"ml-libs": testCandy("ml-libs", spec.CandyModel{}, spec.CandyView{Require: []string{"python"}}),
		"nodejs":  testCandy("nodejs", spec.CandyModel{}, spec.CandyView{}),
		"web-ui":  testCandy("web-ui", spec.CandyModel{}, spec.CandyView{Require: []string{"nodejs"}}),
	}

	tests := []struct {
		name          string
		requested     []string
		parentCandies map[string]bool
		wantOrder     []string
		wantErr       bool
	}{
		{
			name:          "single layer no deps",
			requested:     []string{"pixi"},
			parentCandies: nil,
			wantOrder:     []string{"pixi"},
		},
		{
			name:          "layer with deps",
			requested:     []string{"python"},
			parentCandies: nil,
			wantOrder:     []string{"pixi", "python"},
		},
		{
			name:          "transitive deps",
			requested:     []string{"ml-libs"},
			parentCandies: nil,
			wantOrder:     []string{"pixi", "python", "ml-libs"},
		},
		{
			name:          "multiple independent layers",
			requested:     []string{"pixi", "nodejs"},
			parentCandies: nil,
			wantOrder:     []string{"nodejs", "pixi"}, // sorted alphabetically
		},
		{
			name:          "mixed deps",
			requested:     []string{"ml-libs", "web-ui"},
			parentCandies: nil,
			wantOrder:     []string{"nodejs", "pixi", "python", "ml-libs", "web-ui"},
		},
		{
			name:          "parent provides dep",
			requested:     []string{"python"},
			parentCandies: map[string]bool{"pixi": true},
			wantOrder:     []string{"python"}, // pixi excluded
		},
		{
			name:          "unknown layer",
			requested:     []string{"unknown"},
			parentCandies: nil,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, err := ResolveCandyOrder(tt.requested, layers, tt.parentCandies)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(order, tt.wantOrder) {
				t.Errorf("order = %v, want %v", order, tt.wantOrder)
			}
		})
	}
}

func TestResolveCandyOrderCycle(t *testing.T) {
	// Create candies with a cycle: a -> b -> c -> a
	layers := map[string]spec.CandyReader{
		"a": testCandy("a", spec.CandyModel{}, spec.CandyView{Require: []string{"b"}}),
		"b": testCandy("b", spec.CandyModel{}, spec.CandyView{Require: []string{"c"}}),
		"c": testCandy("c", spec.CandyModel{}, spec.CandyView{Require: []string{"a"}}),
	}

	_, err := ResolveCandyOrder([]string{"a"}, layers, nil)
	if err == nil {
		t.Error("expected cycle error, got nil")
	}

	var cycleErr *CycleError
	if !errors.As(err, &cycleErr) {
		t.Errorf("expected CycleError, got %T", err)
	} else if len(cycleErr.Cycle) == 0 {
		t.Error("CycleError.Cycle is empty")
	}
}

func TestResolveImageOrder(t *testing.T) {
	// Create test boxes
	images := map[string]*buildkit.ResolvedBox{
		"base": {
			Name:           "base",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"cuda": {
			Name:           "cuda",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"ml-cuda": {
			Name:           "ml-cuda",
			Base:           "cuda",
			IsExternalBase: false,
		},
		"inference": {
			Name:           "inference",
			Base:           "ml-cuda",
			IsExternalBase: false,
		},
	}

	order, err := ResolveBoxOrder(images, nil)
	if err != nil {
		t.Fatalf("ResolveBoxOrder() error = %v", err)
	}

	// Check that dependencies come before dependents
	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	// cuda must come before ml-cuda
	if indexOf("cuda") > indexOf("ml-cuda") {
		t.Errorf("cuda should come before ml-cuda, got order %v", order)
	}

	// ml-cuda must come before inference
	if indexOf("ml-cuda") > indexOf("inference") {
		t.Errorf("ml-cuda should come before inference, got order %v", order)
	}
}

func TestResolveImageOrderWithBuilder(t *testing.T) {
	images := map[string]*buildkit.ResolvedBox{
		"builder": {
			Name:           "builder",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"fedora": {
			Name:           "fedora",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
			Builder:        buildkit.BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"app": {
			Name:           "app",
			Base:           "fedora",
			IsExternalBase: false,
			Builder:        buildkit.BuilderMap{"pixi": "builder", "npm": "builder"},
		},
	}

	order, err := ResolveBoxOrder(images, nil)
	if err != nil {
		t.Fatalf("ResolveBoxOrder() error = %v", err)
	}

	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	// builder must come before fedora and app
	if indexOf("builder") > indexOf("fedora") {
		t.Errorf("builder should come before fedora, got order %v", order)
	}
	if indexOf("builder") > indexOf("app") {
		t.Errorf("builder should come before app, got order %v", order)
	}
	// fedora must come before app
	if indexOf("fedora") > indexOf("app") {
		t.Errorf("fedora should come before app, got order %v", order)
	}
}

func TestResolveImageOrderWithBootstrapBuilder(t *testing.T) {
	// Mirrors the cachyos / cachyos-pacstrap-builder pair (relocated to the
	// opencharly/distro-cachyos submodule's charly.yml in the 2026-05 CachyOS migration).
	// `cachyos` is built `from: builder:pacstrap` with
	// `bootstrap_builder_image: cachyos-pacstrap-builder`. A downstream
	// box `app` consumes cachyos via `base: cachyos`. Without the
	// bootstrap-builder edge, the topo-sort would schedule cachyos before
	// cachyos-pacstrap-builder and runPrivilegedBootstrap would fail at
	// resolveLocalImageRef (build.go:294).
	images := map[string]*buildkit.ResolvedBox{
		"arch": {
			Name:           "arch",
			Base:           "docker.io/library/archlinux:latest",
			IsExternalBase: true,
		},
		"cachyos-pacstrap-builder": {
			Name:           "cachyos-pacstrap-builder",
			Base:           "arch",
			IsExternalBase: false,
		},
		"cachyos": {
			Name:                  "cachyos",
			Base:                  "",
			IsExternalBase:        true,
			BootstrapBuilderImage: "cachyos-pacstrap-builder",
		},
		"app": {
			Name:           "app",
			Base:           "cachyos",
			IsExternalBase: false,
		},
	}

	order, err := ResolveBoxOrder(images, nil)
	if err != nil {
		t.Fatalf("ResolveBoxOrder() error = %v", err)
	}

	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	if indexOf("cachyos-pacstrap-builder") > indexOf("cachyos") {
		t.Errorf("cachyos-pacstrap-builder must come before cachyos (bootstrap_builder_image dep), got order %v", order)
	}
	if indexOf("cachyos") > indexOf("app") {
		t.Errorf("cachyos must come before app (base dep), got order %v", order)
	}
	if indexOf("arch") > indexOf("cachyos-pacstrap-builder") {
		t.Errorf("arch must come before cachyos-pacstrap-builder (base dep), got order %v", order)
	}

	// Same property must hold for ResolveBoxLevels (concurrent-build mode).
	levels, err := ResolveBoxLevels(images, nil)
	if err != nil {
		t.Fatalf("ResolveBoxLevels() error = %v", err)
	}
	levelOf := func(name string) int {
		for i, level := range levels {
			if slices.Contains(level, name) {
				return i
			}
		}
		return -1
	}
	if levelOf("cachyos-pacstrap-builder") >= levelOf("cachyos") {
		t.Errorf("cachyos-pacstrap-builder must be in an earlier level than cachyos, got levels %v", levels)
	}
}

func TestResolveImageOrderCycle(t *testing.T) {
	// Create boxes with a cycle
	images := map[string]*buildkit.ResolvedBox{
		"a": {Name: "a", Base: "b", IsExternalBase: false},
		"b": {Name: "b", Base: "c", IsExternalBase: false},
		"c": {Name: "c", Base: "a", IsExternalBase: false},
	}

	_, err := ResolveBoxOrder(images, nil)
	if err == nil {
		t.Error("expected cycle error, got nil")
	}
}

func TestCandiesProvidedByImage(t *testing.T) {
	images := map[string]*buildkit.ResolvedBox{
		"base": {
			Name:           "base",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
			Candy:          []string{"pixi"},
		},
		"cuda": {
			Name:           "cuda",
			Base:           "base",
			IsExternalBase: false,
			Candy:          []string{"cuda"},
		},
		"ml-cuda": {
			Name:           "ml-cuda",
			Base:           "cuda",
			IsExternalBase: false,
			Candy:          []string{"python", "ml-libs"},
		},
	}

	layers := map[string]spec.CandyReader{} // not used, just for type

	tests := []struct {
		name    string
		boxName string
		want    map[string]bool
	}{
		{
			name:    "base image",
			boxName: "base",
			want:    map[string]bool{"pixi": true},
		},
		{
			name:    "cuda inherits from base",
			boxName: "cuda",
			want:    map[string]bool{"pixi": true, "cuda": true},
		},
		{
			name:    "ml-cuda inherits from cuda",
			boxName: "ml-cuda",
			want:    map[string]bool{"pixi": true, "cuda": true, "python": true, "ml-libs": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CandyProvidedByBox(tt.boxName, images, layers)
			if err != nil {
				t.Fatalf("CandyProvidedByBox() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CandyProvidedByBox() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveCandyOrderWithComposition(t *testing.T) {
	layers := map[string]spec.CandyReader{
		"pixi":        testCandy("pixi", spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}}, spec.CandyView{}),
		"python":      testCandy("python", spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}}, spec.CandyView{Require: []string{"pixi"}}),
		"supervisord": testCandy("supervisord", spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}}, spec.CandyView{Require: []string{"python"}}),
		"svc-stack":   testCandy("svc-stack", spec.CandyModel{}, spec.CandyView{IncludedCandy: []string{"python", "supervisord"}}),
	}

	order, err := ResolveCandyOrder([]string{"svc-stack"}, layers, nil)
	if err != nil {
		t.Fatalf("ResolveCandyOrder() error: %v", err)
	}
	// pixi (dep of python) → python → supervisord
	want := []string{"pixi", "python", "supervisord"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestDependsOnComposingCandy(t *testing.T) {
	layers := map[string]spec.CandyReader{
		"pipewire":     testCandy("pipewire", spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}}, spec.CandyView{}),
		"wayvnc":       testCandy("wayvnc", spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}}, spec.CandyView{}),
		"sway-desktop": testCandy("sway-desktop", spec.CandyModel{}, spec.CandyView{IncludedCandy: []string{"pipewire", "wayvnc"}}),
		"myapp":        testCandy("myapp", spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}}, spec.CandyView{Require: []string{"sway-desktop"}}),
	}

	order, err := ResolveCandyOrder([]string{"myapp"}, layers, nil)
	if err != nil {
		t.Fatalf("ResolveCandyOrder() error: %v", err)
	}
	// pipewire and wayvnc should be pulled in via sway-desktop dependency
	want := []string{"pipewire", "wayvnc", "myapp"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestResolveImageLevels(t *testing.T) {
	images := map[string]*buildkit.ResolvedBox{
		"base": {
			Name:           "base",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"cuda": {
			Name:           "cuda",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"app": {
			Name:           "app",
			Base:           "base",
			IsExternalBase: false,
		},
		"ml": {
			Name:           "ml",
			Base:           "cuda",
			IsExternalBase: false,
		},
		"inference": {
			Name:           "inference",
			Base:           "ml",
			IsExternalBase: false,
		},
	}

	levels, err := ResolveBoxLevels(images, nil)
	if err != nil {
		t.Fatalf("ResolveBoxLevels() error = %v", err)
	}

	// Level 0: base, cuda (no deps)
	// Level 1: app (depends on base), ml (depends on cuda)
	// Level 2: inference (depends on ml)
	if len(levels) != 3 {
		t.Fatalf("expected 3 levels, got %d: %v", len(levels), levels)
	}
	if !reflect.DeepEqual(levels[0], []string{"base", "cuda"}) {
		t.Errorf("level 0 = %v, want [base cuda]", levels[0])
	}
	if !reflect.DeepEqual(levels[1], []string{"app", "ml"}) {
		t.Errorf("level 1 = %v, want [app ml]", levels[1])
	}
	if !reflect.DeepEqual(levels[2], []string{"inference"}) {
		t.Errorf("level 2 = %v, want [inference]", levels[2])
	}
}
