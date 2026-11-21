package main

import (
	"context"
	"slices"
	"testing"

	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// content_kinds_test.go — the host half of ops.OpContent (opencharly/sdk#598): a class:kind capability's
// `contents` declaration is recorded off the wire, and the validate-word-sets leg publishes exactly the
// kinds that declare it as content_kinds — read off each provider's own capability, never a per-kind
// branch (the boundary law). The build kit asks exactly these kinds; one missing would build images
// silently without their skills.

type stubContentKind struct{ contents bool }

func (s stubContentKind) Reserved() string {
	if s.contents {
		return "stubcontentkind"
	}
	return "stubplainkind"
}
func (stubContentKind) Class() ProviderClass  { return ClassKind }
func (s stubContentKind) IsContentKind() bool { return s.contents }
func (stubContentKind) Invoke(context.Context, *Operation) (*Result, error) {
	return &Result{JSON: []byte(`{}`)}, nil
}

func TestValidateWordSets_PublishesTheDeclaringContentKinds(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if err := providerRegistry.register(stubContentKind{contents: true}, "test:stubcontentkind"); err != nil {
		t.Fatal(err)
	}
	if err := providerRegistry.register(stubContentKind{contents: false}, "test:stubplainkind"); err != nil {
		t.Fatal(err)
	}
	reply, err := hostBuildValidateWordSets(context.Background(), spec.ValidateWordSetsRequest{}, buildEngineContext{})
	if err != nil {
		t.Fatalf("validate-word-sets: %v", err)
	}
	if !slices.Contains(reply.ContentKinds, "stubcontentkind") {
		t.Fatalf("content_kinds = %v, want the kind declaring contents", reply.ContentKinds)
	}
	if slices.Contains(reply.ContentKinds, "stubplainkind") {
		t.Fatalf("content_kinds = %v, a kind that does not declare contents was published", reply.ContentKinds)
	}
	if !slices.IsSorted(reply.ContentKinds) {
		t.Fatalf("content_kinds = %v, want sorted (a reproducible build order)", reply.ContentKinds)
	}
}

// The flag is recorded only for a class:kind capability, from the wire declaration.
func TestBuildCapMeta_RecordsContentsForAKindOnly(t *testing.T) {
	kind, err := buildCapMeta(&pb.ProvidedCapability{Class: "kind", Word: "skill", InputDef: "#SkillInput", Contents: true})
	if err != nil || !kind.IsContentKind() {
		t.Fatalf("kind:skill with contents: IsContentKind = %v (%v), want true", kind.IsContentKind(), err)
	}
	verb, err := buildCapMeta(&pb.ProvidedCapability{Class: "verb", Word: "x", InputDef: "#XInput", Contents: true})
	if err != nil || verb.IsContentKind() {
		t.Fatalf("a verb declaring contents: IsContentKind = %v (%v), want false — only a kind serves owned content", verb.IsContentKind(), err)
	}
}
