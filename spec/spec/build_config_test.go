package spec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestDistroConfigRoundTrip proves the relocated #DistroConfig container
// JSON-round-trips and its vocabulary-resolution methods (ResolveDistro,
// inherits-chain, format lookup) behave as they did in sdk/buildkit. #55 step
// 3-III relocation coverage.
func TestDistroConfigRoundTrip(t *testing.T) {
	dc := &DistroConfig{Distro: map[string]*ResolvedDistro{
		"base": {
			Version:     "1",
			Bootstrap:   Bootstrap{InstallCmd: "bootstrap-base"},
			Workarounds: []string{"wa"},
			Format: map[string]*Format{
				"rpm": {InstallTemplate: "dnf install"},
			},
		},
		"fedora": {
			Inherits: "base",
			Format: map[string]*Format{
				"deb": {InstallTemplate: "apt install"},
			},
		},
	}}

	b, err := json.Marshal(dc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got DistroConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(dc.Distro, got.Distro) {
		t.Fatalf("round-trip mismatch:\n want %+v\n got  %+v", dc.Distro, got.Distro)
	}

	// ResolveDistro by exact tag inherits the base bootstrap.
	res := dc.ResolveDistro([]string{"fedora"})
	if res == nil {
		t.Fatal("ResolveDistro(fedora) = nil")
	}
	if res.Bootstrap.InstallCmd != "bootstrap-base" {
		t.Errorf("inherited bootstrap = %q, want bootstrap-base", res.Bootstrap.InstallCmd)
	}
	// fedora overrides Format with deb only (child has no bootstrap → child formats win).
	if _, ok := res.Format["deb"]; !ok {
		t.Errorf("resolved fedora missing own format deb: %+v", res.Format)
	}

	// AllFormatNames is the sorted union across distros.
	got2 := dc.AllFormatNames()
	want := []string{"deb", "rpm"}
	if !reflect.DeepEqual(got2, want) {
		t.Errorf("AllFormatNames = %v, want %v", got2, want)
	}
	if !dc.ValidFormat("rpm") || dc.ValidFormat("nope") {
		t.Errorf("ValidFormat wrong: rpm=%v nope=%v", dc.ValidFormat("rpm"), dc.ValidFormat("nope"))
	}

	// WrapDistroDef + DistroTagChain helpers.
	if w := WrapDistroDef(res); w.FindFormat("deb") == nil {
		t.Error("WrapDistroDef.FindFormat(deb) = nil")
	}
	if chain := DistroTagChain("ubuntu", "24.04"); !reflect.DeepEqual(chain, []string{"ubuntu:24.04", "ubuntu"}) {
		t.Errorf("DistroTagChain = %v", chain)
	}
}

// TestBuilderConfigRoundTrip proves the relocated #BuilderConfig container and
// its ValidBuilderType/BuilderNames methods.
func TestBuilderConfigRoundTrip(t *testing.T) {
	bc := &BuilderConfig{Builder: map[string]*Builder{
		"go":     {Kind: "layer"},
		"cargo":  {Kind: "layer"},
		"golang": {Kind: "bootstrap"},
	}}
	b, err := json.Marshal(bc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got BuilderConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(bc.Builder, got.Builder) {
		t.Fatalf("round-trip mismatch")
	}
	if !bc.ValidBuilderType("go") || bc.ValidBuilderType("nope") {
		t.Error("ValidBuilderType wrong")
	}
	if names := bc.BuilderNames(); !reflect.DeepEqual(names, []string{"cargo", "go", "golang"}) {
		t.Errorf("BuilderNames = %v, want sorted", names)
	}
}
