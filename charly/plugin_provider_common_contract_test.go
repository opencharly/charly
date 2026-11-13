package main

// plugin_provider_common_contract_test.go — the WIRE→carrier half of a class:step contract: every
// field a plugin DECLARES must reach the host's carrier, because the host acts on the carrier and
// never on the raw wire struct.
//
// The test exists because a field can be added to the wire and to every sibling conversion and
// still be dropped HERE, which makes the plugin's declaration invisible while everything compiles.
// Measured (opencharly/charly#1131): PluginPaths was on the wire, in `sdk`'s spec→pb build and in
// `spec/exec`'s pb→spec decode, and this third conversion dropped it — so a plugin declared the
// flag, the host read false, and the step never received the connected plugin set.

import (
	"testing"

	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

func TestBuildCapMetaCarriesEveryDeclaredStepContractField(t *testing.T) {
	meta, err := buildCapMeta(&pb.ProvidedCapability{
		Class: "step",
		Word:  "plugin-tree",
		StepContract: &pb.StepContract{
			Scope:       spec.ScopeSystem.String(),
			Venue:       int32(spec.VenueHostNative),
			Gate:        string(spec.GateAllowRootTasks),
			Emits:       true,
			PluginPaths: true,
		},
	})
	if err != nil {
		t.Fatalf("buildCapMeta: %v", err)
	}
	contract, ok := meta.DeclaredStepContract()
	if !ok {
		t.Fatal("the carrier reports no declared contract for a class:step capability that declares one")
	}
	if !contract.PluginPaths {
		t.Fatal("PluginPaths was dropped in the wire→carrier conversion: the host would never inject the connected plugin set this step declared it needs")
	}
	if contract.Scope != spec.ScopeSystem || contract.Venue != spec.VenueHostNative || contract.Gate != spec.GateAllowRootTasks || !contract.Emits {
		t.Fatalf("the contract's other fields drifted: %+v", contract)
	}

	// A provider that declares NO contract must not acquire one — the flag can only travel on a
	// contract that exists.
	meta, err = buildCapMeta(&pb.ProvidedCapability{Class: "step", Word: "no-contract"})
	if err != nil {
		t.Fatalf("buildCapMeta: %v", err)
	}
	if _, ok := meta.DeclaredStepContract(); ok {
		t.Fatal("a class:step capability with no StepContract reports one")
	}
}
