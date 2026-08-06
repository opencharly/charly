package fleet

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// volumes_test.go — relocated from charly/volumes_test.go (#55 decoupling, Batch A): all 4
// tests assert deploykit.CollectBoxVolume directly, zero charly dep.

func TestCollectImageVolumesSimple(t *testing.T) {
	cfg := &spec.Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"myapp": {Candy: []string{"svc"}},
		}),
	}
	layers := map[string]spec.CandyReader{
		"svc": testCandy("svc",
			spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}},
			spec.CandyView{Volumes: []vmshared.VolumeYAML{{Name: "data", Path: "~/.myapp"}}},
		),
	}

	mounts, err := deploykit.CollectBoxVolume(cfg, layers, "myapp", "/home/user", nil)
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
	cfg := &spec.Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"base":  {Candy: []string{"store"}},
			"child": {Base: "base", Candy: []string{"app"}},
		}),
	}
	layers := map[string]spec.CandyReader{
		"store": testCandy("store",
			spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}},
			spec.CandyView{Volumes: []vmshared.VolumeYAML{{Name: "models", Path: "~/.models"}}},
		),
		"app": testCandy("app",
			spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}},
			spec.CandyView{Volumes: []vmshared.VolumeYAML{{Name: "data", Path: "~/.app"}}},
		),
	}

	mounts, err := deploykit.CollectBoxVolume(cfg, layers, "child", "/home/user", nil)
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
	cfg := &spec.Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"base":  {Candy: []string{"store"}},
			"child": {Base: "base", Candy: []string{"override"}},
		}),
	}
	layers := map[string]spec.CandyReader{
		"store": testCandy("store",
			spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}},
			spec.CandyView{Volumes: []vmshared.VolumeYAML{{Name: "data", Path: "~/.base-data"}}},
		),
		"override": testCandy("override",
			spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}},
			spec.CandyView{Volumes: []vmshared.VolumeYAML{{Name: "data", Path: "~/.child-data"}}},
		),
	}

	mounts, err := deploykit.CollectBoxVolume(cfg, layers, "child", "/home/user", nil)
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
	cfg := &spec.Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"base": {Candy: []string{"plain"}},
		}),
	}
	layers := map[string]spec.CandyReader{
		"plain": testCandy("plain", spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}}, spec.CandyView{}),
	}

	mounts, err := deploykit.CollectBoxVolume(cfg, layers, "base", "/home/user", nil)
	if err != nil {
		t.Fatalf("CollectBoxVolume() error = %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("expected 0 mounts, got %v", mounts)
	}
}
