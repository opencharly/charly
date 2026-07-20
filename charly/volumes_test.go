package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

func TestCollectImageVolumesSimple(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"myapp": {Candy: []string{"svc"}},
		}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:    "svc",
			plan:    []spec.Step{{Run: "build", Op: cmdOp("true")}},
			volumes: []VolumeYAML{{Name: "data", Path: "~/.myapp"}},
		},
	}

	mounts, err := CollectBoxVolume(cfg, layers, "myapp", "/home/user", nil)
	if err != nil {
		t.Fatalf("CollectBoxVolume() error = %v", err)
	}

	want := []deploykit.VolumeMount{
		{VolumeName: "charly-myapp-data", ContainerPath: "/home/user/.myapp"},
	}
	if !reflect.DeepEqual(mounts, want) {
		t.Errorf("CollectBoxVolume() =\n  %v\nwant\n  %v", mounts, want)
	}
}

func TestCollectImageVolumesChain(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"base":  {Candy: []string{"store"}},
			"child": {Base: "base", Candy: []string{"app"}},
		}),
	}
	layers := map[string]*Candy{
		"store": {
			Name:    "store",
			plan:    []spec.Step{{Run: "build", Op: cmdOp("true")}},
			volumes: []VolumeYAML{{Name: "models", Path: "~/.models"}},
		},
		"app": {
			Name:    "app",
			plan:    []spec.Step{{Run: "build", Op: cmdOp("true")}},
			volumes: []VolumeYAML{{Name: "data", Path: "~/.app"}},
		},
	}

	mounts, err := CollectBoxVolume(cfg, layers, "child", "/home/user", nil)
	if err != nil {
		t.Fatalf("CollectBoxVolume() error = %v", err)
	}

	// Should have volumes from both child and base image candies
	want := []deploykit.VolumeMount{
		{VolumeName: "charly-child-data", ContainerPath: "/home/user/.app"},
		{VolumeName: "charly-child-models", ContainerPath: "/home/user/.models"},
	}
	if !reflect.DeepEqual(mounts, want) {
		t.Errorf("CollectBoxVolume() =\n  %v\nwant\n  %v", mounts, want)
	}
}

func TestCollectImageVolumesDedup(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"base":  {Candy: []string{"store"}},
			"child": {Base: "base", Candy: []string{"override"}},
		}),
	}
	layers := map[string]*Candy{
		"store": {
			Name:    "store",
			plan:    []spec.Step{{Run: "build", Op: cmdOp("true")}},
			volumes: []VolumeYAML{{Name: "data", Path: "~/.base-data"}},
		},
		"override": {
			Name:    "override",
			plan:    []spec.Step{{Run: "build", Op: cmdOp("true")}},
			volumes: []VolumeYAML{{Name: "data", Path: "~/.child-data"}},
		},
	}

	mounts, err := CollectBoxVolume(cfg, layers, "child", "/home/user", nil)
	if err != nil {
		t.Fatalf("CollectBoxVolume() error = %v", err)
	}

	// First declaration wins (outermost image)
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d: %v", len(mounts), mounts)
	}
	if mounts[0].ContainerPath != "/home/user/.child-data" {
		t.Errorf("expected child override to win, got path %q", mounts[0].ContainerPath)
	}
}

func TestCollectImageVolumesNoVolumes(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"base": {Candy: []string{"plain"}},
		}),
	}
	layers := map[string]*Candy{
		"plain": {Name: "plain", plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}},
	}

	mounts, err := CollectBoxVolume(cfg, layers, "base", "/home/user", nil)
	if err != nil {
		t.Fatalf("CollectBoxVolume() error = %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("expected 0 mounts, got %v", mounts)
	}
}

func TestExpandHome(t *testing.T) {
	tests := []struct {
		path string
		home string
		want string
	}{
		{"~/.openclaw", "/home/user", "/home/user/.openclaw"},
		{"~", "/home/user", "/home/user"},
		{"$HOME/.config", "/home/user", "/home/user/.config"},
		{"/absolute/path", "/home/user", "/absolute/path"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := expandHome(tt.path, tt.home)
			if got != tt.want {
				t.Errorf("expandHome(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
			}
		})
	}
}

func TestSortVolumeMounts(t *testing.T) {
	mounts := []deploykit.VolumeMount{
		{VolumeName: "charly-app-z", ContainerPath: "/z"},
		{VolumeName: "charly-app-a", ContainerPath: "/a"},
		{VolumeName: "charly-app-m", ContainerPath: "/m"},
	}
	sortVolumeMounts(mounts)
	if mounts[0].VolumeName != "charly-app-a" || mounts[1].VolumeName != "charly-app-m" || mounts[2].VolumeName != "charly-app-z" {
		t.Errorf("sortVolumeMounts() result: %v", mounts)
	}
}
