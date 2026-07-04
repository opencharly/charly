package main

import "testing"

// A declared external kind whose out-of-process provider FAILED to build/connect (a minimal
// container with no Go toolchain, a broken plugin) must NOT hard-error the whole load — read-only
// commands (box list, validate) still need to work in that degraded environment. normalizeNodeInto
// gracefully warn-SKIPS the node instead. Without the fix it returned a hard error.
func TestExternalKind_UnconnectedProviderWarnSkips(t *testing.T) {
	const kind = "brokentestkind-graceful-skip-fixture"

	// Declare the kind WITHOUT connecting a provider — recognizedKind is true (declared),
	// but ResolveKind fails (no provider), exactly the failed-build/connect state.
	registerDeclaredKind(kind)
	defer func() {
		declaredDeployMu.Lock()
		delete(declaredKind, kind)
		declaredDeployMu.Unlock()
	}()

	// Preconditions: OUTSIDE the connect pre-pass (the default) with no connected provider —
	// the exact "provider did not connect" condition.
	if inKindConnectPass() {
		t.Fatal("test precondition: must run outside the connect pre-pass")
	}
	if _, ok := providerRegistry.ResolveKind(kind); ok {
		t.Fatalf("test precondition: %q must have NO connected provider", kind)
	}

	// normalizeNodeInto on such a node WARN+SKIPS (nil), never a hard error. (A node with no
	// member children never reaches the member-nesting parse gate, so the graceful skip is the
	// operative path for the common flat-declared-kind case — proven live via `box list boxes`.)
	gn := &genericNode{name: "my-broken-entity", disc: kind, discClass: "entity"}
	if err := normalizeNodeInto(gn, &UnifiedFile{}); err != nil {
		t.Fatalf("normalizeNodeInto for an unconnected declared kind = %v; want nil (graceful warn+skip so read-only commands work)", err)
	}
}
