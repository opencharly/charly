package main

import (
	"context"
	"strings"
	"testing"

	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// zzReqProv is a minimal in-proc provider that satisfies the registry (a distinct
// class+word per test so registrations never collide across the suite).
type zzReqProv struct {
	class ProviderClass
	word  string
}

func (p zzReqProv) Reserved() string                                    { return p.word }
func (p zzReqProv) Class() ProviderClass                                { return p.class }
func (p zzReqProv) Invoke(context.Context, *Operation) (*Result, error) { return &Result{}, nil }

// reqCap renders a peer identity "<class>:<word>[:<parent>]" for a requirement fixture.
func reqCap(class ProviderClass, word string) spec.PluginCapability {
	return spec.PluginCapability(providerKey(class, word, ""))
}

// A declared, already-registered peer resolves with no work: the gate passes and the
// unit is not blocked. This is the common case (a peer in the same project/registry).
func TestRegisterPluginUnitRequires_PeerAlreadyRegistered(t *testing.T) {
	providerRegistry.register(zzReqProv{ClassVerb, "zzreqpresent"}, "test")
	unit := &PluginUnit{
		Providers: []Provider{zzReqProv{ClassVerb, "zzreqconsumer"}},
		Requires:  []spec.PluginRequirement{{Capability: reqCap(ClassVerb, "zzreqpresent")}},
	}
	if err := registerPluginUnitRequires("zzreqconsumer", unit); err != nil {
		t.Fatalf("declared+registered peer must resolve, got %v", err)
	}
}

// An unresolvable NON-optional requirement is a hard error NAMING the plugin and the
// peer (the loud-at-load contract, mirroring the schema gate).
func TestRegisterPluginUnitRequires_MissingPeerFailsLoud(t *testing.T) {
	unit := &PluginUnit{
		Providers: []Provider{zzReqProv{ClassVerb, "zzreqmissconsumer"}},
		Requires:  []spec.PluginRequirement{{Capability: reqCap(ClassVerb, "zzreqabsent-peer")}},
	}
	err := registerPluginUnitRequires("zzreqmissconsumer", unit)
	if err == nil {
		t.Fatal("a missing non-optional peer must fail the load, got nil")
	}
	if !strings.Contains(err.Error(), "zzreqmissconsumer") || !strings.Contains(err.Error(), "zzreqabsent-peer") {
		t.Fatalf("error must name both the plugin and the missing peer, got %q", err.Error())
	}
}

// An absent OPTIONAL requirement is a skip, not a failure.
func TestRegisterPluginUnitRequires_OptionalMissingSkips(t *testing.T) {
	unit := &PluginUnit{
		Providers: []Provider{zzReqProv{ClassVerb, "zzreqoptconsumer"}},
		Requires:  []spec.PluginRequirement{{Capability: reqCap(ClassVerb, "zzreq-opt-absent"), Optional: true}},
	}
	if err := registerPluginUnitRequires("zzreqoptconsumer", unit); err != nil {
		t.Fatalf("an absent optional peer must skip, got %v", err)
	}
}

// A declared dependency CYCLE is rejected with the chain named, rather than recursing
// or hanging. Simulated by marking B as in-flight (as if B were mid-load) while A
// requires B.
func TestRegisterPluginUnitRequires_CycleRejected(t *testing.T) {
	requiresGateMu.Lock()
	requiresInFlight[provKey(ClassVerb, "zzreqcycle-b")] = "zzreqcycle-b-provider"
	requiresGateMu.Unlock()
	defer func() {
		requiresGateMu.Lock()
		delete(requiresInFlight, provKey(ClassVerb, "zzreqcycle-b"))
		requiresGateMu.Unlock()
	}()

	unit := &PluginUnit{
		Providers: []Provider{zzReqProv{ClassVerb, "zzreqcycle-a"}},
		Requires:  []spec.PluginRequirement{{Capability: reqCap(ClassVerb, "zzreqcycle-b")}},
	}
	err := registerPluginUnitRequires("zzreqcycle-a", unit)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("a declared cycle must be rejected naming the cycle, got %v", err)
	}
}

// A requirement naming a NESTED command carries its parent, so the gate resolves the
// full identity `<class>:<word>:<parent>` (the C0 key grammar) — a top-level same-word
// twin must NOT satisfy it.
func TestRegisterPluginUnitRequires_NestedIdentityResolvesParent(t *testing.T) {
	// Register a TOP-LEVEL zzreqnested — it must NOT satisfy a nested requirement.
	if err := providerRegistry.register(zzReqProv{ClassCommand, "zzreqnested"}, "test"); err != nil {
		t.Fatalf("register top-level: %v", err)
	}
	unit := &PluginUnit{
		Providers: []Provider{zzReqProv{ClassCommand, "zzreqnestedconsumer"}},
		Requires:  []spec.PluginRequirement{{Capability: spec.PluginCapability(providerKey(ClassCommand, "zzreqnested", "zzreqbox"))}},
	}
	// The nested identity (command:zzreqnested:zzreqbox) is NOT registered → a non-optional
	// miss, proving the gate looked up the PARENTED key, not the bare word.
	err := registerPluginUnitRequires("zzreqnestedconsumer", unit)
	if err == nil {
		t.Fatal("a nested requirement must not be satisfied by the top-level same-word twin")
	}
	if !strings.Contains(err.Error(), "command:zzreqnested:zzreqbox") {
		t.Fatalf("the error must name the nested IDENTITY, got %q", err.Error())
	}
}

// liftRequirements rejects a malformed requirement (unknown class / empty word) with
// the SAME closed class vocabulary the capability lift uses, and re-renders the peer
// as the ONE capability identity (providerKey).
func TestLiftRequirements_RejectsMalformed(t *testing.T) {
	if _, err := liftRequirements([]*pb.PluginRequirement{{Class: "bogus", Word: "x"}}, "test"); err == nil {
		t.Error("an unknown class must be rejected")
	}
	if _, err := liftRequirements([]*pb.PluginRequirement{{Class: "verb", Word: ""}}, "test"); err == nil {
		t.Error("an empty word must be rejected")
	}
	if rs, err := liftRequirements(nil, "test"); err != nil || rs != nil {
		t.Errorf("no requirements must lift to (nil,nil), got (%v,%v)", rs, err)
	}
	rs, err := liftRequirements([]*pb.PluginRequirement{{Class: "command", Word: "feature", CommandParent: "box", Source: "gh/x", Optional: true}}, "test")
	if err != nil || len(rs) != 1 {
		t.Fatalf("lift one: %v %v", rs, err)
	}
	if rs[0].Capability != "command:feature:box" || rs[0].Source != "gh/x" || !rs[0].Optional {
		t.Fatalf("lifted requirement = %+v, want the parented identity command:feature:box", rs[0])
	}
}

// The AUTHORED manifest declaration (`plugin.requires:`, read from the resolved view) is
// THE declared-dependency source for a scanned candy. These fixtures carry NO wire
// requires, so only a manifest read (candy.GetPluginRequires) can make them pass/fail —
// they fail if gateCandyRequires stops reading the manifest.

func TestGateCandyRequires_RegisteredPeerResolves(t *testing.T) {
	if err := providerRegistry.register(zzReqProv{ClassVerb, "zzgm-present"}, "test"); err != nil {
		t.Fatalf("register peer: %v", err)
	}
	candy := testCandy("zzgm-consumer", spec.CandyModel{}, spec.CandyView{
		IsPlugin:        true,
		PluginSource:    "github.com/opencharly/zzgm-consumer",
		PluginProviders: []string{"verb:zzgm-consumer"},
		PluginRequires:  []spec.PluginRequirement{{Capability: reqCap(ClassVerb, "zzgm-present")}},
	})
	if err := gateCandyRequires("zzgm-consumer", candy); err != nil {
		t.Fatalf("a manifest requirement on a registered peer must resolve, got %v", err)
	}
}

func TestGateCandyRequires_MissingPeerFailsLoud(t *testing.T) {
	candy := testCandy("zzgm-miss", spec.CandyModel{}, spec.CandyView{
		IsPlugin:        true,
		PluginSource:    "github.com/opencharly/zzgm-miss",
		PluginProviders: []string{"verb:zzgm-miss"},
		PluginRequires:  []spec.PluginRequirement{{Capability: reqCap(ClassVerb, "zzgm-absent")}},
	})
	err := gateCandyRequires("zzgm-miss", candy)
	if err == nil || !strings.Contains(err.Error(), "zzgm-absent") {
		t.Fatalf("a manifest requirement on a missing peer must fail loud, got %v", err)
	}
}

func TestGateCandyRequires_NoManifestRequiresNoOp(t *testing.T) {
	candy := testCandy("zzgm-none", spec.CandyModel{}, spec.CandyView{
		IsPlugin:        true,
		PluginSource:    "github.com/opencharly/zzgm-none",
		PluginProviders: []string{"verb:zzgm-none"},
	})
	if err := gateCandyRequires("zzgm-none", candy); err != nil {
		t.Fatalf("a candy declaring no manifest requires must be a no-op, got %v", err)
	}
}

// The compiled-in placement: a candy whose providers are already registered as builtin (the
// `compiled_plugins:` selection) is served in-proc, but its AUTHORED manifest declaration is
// STILL gated in loadProjectPlugins — where the scanned candy is in scope — so a compiled-in
// plugin's declared peer is resolved exactly as an external one's.
func TestLoadProjectPlugins_CompiledInCandyManifestGated(t *testing.T) {
	if err := providerRegistry.register(zzReqProv{ClassVerb, "zzgmcompiledin"}, originBuiltin); err != nil {
		t.Fatalf("register builtin provider: %v", err)
	}
	candy := testCandy("zzgmcompiledin-candy", spec.CandyModel{}, spec.CandyView{
		IsPlugin:        true,
		PluginSource:    "github.com/opencharly/zzgmcompiledin",
		PluginProviders: []string{"verb:zzgmcompiledin"},
		PluginRequires:  []spec.PluginRequirement{{Capability: reqCap(ClassVerb, "zzgmcompiledin-missing")}},
	})
	candies := map[string]spec.CandyReader{"zzgmcompiledin-candy": candy}
	refs := map[string]struct{}{"zzgmcompiledin": {}}
	err := loadProjectPlugins(context.Background(), candies, refs)
	if err == nil || !strings.Contains(err.Error(), "zzgmcompiledin-missing") {
		t.Fatalf("a compiled-in candy's manifest requires must be gated by loadProjectPlugins, got %v", err)
	}
}
