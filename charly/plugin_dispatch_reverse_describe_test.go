package main

import (
	"context"
	"testing"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// plugin_dispatch_reverse_describe_test.go — K5-A item 2: proves the DescribeProvider RPC round
// trip end-to-end (host handler -> generated gRPC message shapes -> the SAME decode a real client
// would run), the "spike-then-build" evidence the F11-reviewable new surface needed BEFORE
// oci_step_emit.go's relocation depends on it.

// describeStepFake is a minimal Provider implementing spec.StepContractCarrier — the SAME shape
// capMeta gives every real class:step provider (buildCapMeta), without needing to build+connect an
// out-of-process plugin binary just to prove the wire round trip.
type describeStepFake struct {
	word     string
	contract spec.StepContract
}

func (f describeStepFake) Reserved() string     { return f.word }
func (f describeStepFake) Class() ProviderClass { return ClassStep }
func (f describeStepFake) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, nil
}
func (f describeStepFake) DeclaredStepContract() (spec.StepContract, bool) {
	return f.contract, true
}

var _ spec.StepContractCarrier = describeStepFake{}

// TestDescribeProvider_FoundWithStepContract proves the happy path: a registered class:step
// provider's DECLARED StepContract round-trips through the wire message shapes byte-for-byte.
func TestDescribeProvider_FoundWithStepContract(t *testing.T) {
	isolateProviderRegistry(t)
	fake := describeStepFake{word: "describe-fake-step", contract: spec.StepContract{
		Scope: spec.ScopeUser, Venue: spec.VenueHostNative, Gate: spec.GateAllowRepoChanges, Emits: true,
	}}
	if err := providerRegistry.register(fake, originBuiltin); err != nil {
		t.Fatalf("register fake provider: %v", err)
	}

	s := &executorReverseServer{}
	reply, err := s.DescribeProvider(context.Background(), &pb.DescribeProviderRequest{Class: string(ClassStep), Word: "describe-fake-step"})
	if err != nil {
		t.Fatalf("DescribeProvider: %v", err)
	}
	if !reply.GetFound() {
		t.Fatal("Found = false, want true for a registered provider")
	}
	sc := reply.GetStepContract()
	if sc == nil {
		t.Fatal("StepContract = nil, want the fake's declared contract")
	}
	if sc.GetScope() != fake.contract.Scope.String() {
		t.Errorf("Scope = %q, want %q", sc.GetScope(), fake.contract.Scope.String())
	}
	if sc.GetVenue() != int32(fake.contract.Venue) {
		t.Errorf("Venue = %d, want %d", sc.GetVenue(), fake.contract.Venue)
	}
	if sc.GetGate() != string(fake.contract.Gate) {
		t.Errorf("Gate = %q, want %q", sc.GetGate(), fake.contract.Gate)
	}
	if sc.GetEmits() != fake.contract.Emits {
		t.Errorf("Emits = %v, want %v", sc.GetEmits(), fake.contract.Emits)
	}
}

// TestDescribeProvider_NotFound proves the "no connected provider" outcome is a QUERY RESULT
// (found=false), never an RPC error — the property a routing decision needs (distinguish
// not-found from a transport failure).
func TestDescribeProvider_NotFound(t *testing.T) {
	isolateProviderRegistry(t)
	s := &executorReverseServer{}
	reply, err := s.DescribeProvider(context.Background(), &pb.DescribeProviderRequest{Class: string(ClassStep), Word: "no-such-word-ever-registered"})
	if err != nil {
		t.Fatalf("DescribeProvider: unexpected error %v (want a found=false result, not an error)", err)
	}
	if reply.GetFound() {
		t.Fatal("Found = true, want false for an unregistered (class, word)")
	}
	if reply.GetStepContract() != nil {
		t.Errorf("StepContract = %+v, want nil when not found", reply.GetStepContract())
	}
}

// TestDescribeProvider_FoundNoStepContract proves a resolvable provider that does NOT declare a
// StepContract (e.g. a class:verb capability) reports found=true with a nil StepContract —
// distinguishing "no such provider" from "provider exists but declares nothing here", exactly
// mirroring ProvidedCapability.step_contract's own per-class optionality.
func TestDescribeProvider_FoundNoStepContract(t *testing.T) {
	isolateProviderRegistry(t)
	// A builtin verb provider is registered but does NOT implement spec.StepContractCarrier with a
	// true ok — the "file" verb (a plain ProvisionActor) is exactly this shape in production; reuse
	// it directly rather than hand-rolling a second fake.
	prov, ok := providerRegistry.ResolveVerb("file")
	if !ok {
		t.Skip("builtin verb \"file\" is not registered in this build")
	}
	_ = prov

	s := &executorReverseServer{}
	reply, err := s.DescribeProvider(context.Background(), &pb.DescribeProviderRequest{Class: string(ClassVerb), Word: "file"})
	if err != nil {
		t.Fatalf("DescribeProvider: %v", err)
	}
	if !reply.GetFound() {
		t.Fatal("Found = false, want true for the registered \"file\" verb")
	}
	if reply.GetStepContract() != nil {
		t.Errorf("StepContract = %+v, want nil — the file verb is not a class:step capability", reply.GetStepContract())
	}
}

// TestDescribeProvider_ViaInProcExecutorClient proves the CLIENT-side sdk.Executor.DescribeProvider
// method + the inprocExecutorClient bridge round-trip correctly (the SAME "compiled-in in-proc
// reverse channel" path oci_step_emit.go's relocation to candy/plugin-installstep will drive) —
// not just the raw server method in isolation.
func TestDescribeProvider_ViaInProcExecutorClient(t *testing.T) {
	isolateProviderRegistry(t)
	fake := describeStepFake{word: "describe-fake-step-2", contract: spec.StepContract{
		Scope: spec.ScopeSystem, Venue: spec.VenueContainerBuilder, Gate: spec.GateWithServices, Emits: false,
	}}
	if err := providerRegistry.register(fake, originBuiltin); err != nil {
		t.Fatalf("register fake provider: %v", err)
	}

	ctx, ex := testConstructStepExecutor()
	found, contract, err := ex.DescribeProvider(ctx, string(ClassStep), "describe-fake-step-2")
	if err != nil {
		t.Fatalf("Executor.DescribeProvider: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if contract == nil {
		t.Fatal("contract = nil, want the fake's declared contract")
	}
	if contract.Scope != fake.contract.Scope.String() || contract.Venue != int(fake.contract.Venue) ||
		contract.Gate != string(fake.contract.Gate) || contract.Emits != fake.contract.Emits {
		t.Errorf("contract = %+v, want {Scope:%q Venue:%d Gate:%q Emits:%v}",
			contract, fake.contract.Scope.String(), fake.contract.Venue, fake.contract.Gate, fake.contract.Emits)
	}
}
