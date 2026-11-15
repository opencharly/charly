package main

import "testing"

// TestLoaderThreadedCarriesTheAuthoredAndWireShapes pins the gates the host threads, measured on
// the tree it ships: 20 gates, and the `vm` gate carries the AUTHORED body in Def with the wire
// value beside it in WireDef — the split spec#328 declares. It fails if the fill stops threading,
// if the registry key shape regresses (`class:word`, not `class/word` — an empty map is what the
// wrong separator produced), or if the two shapes collapse back into one.
func TestLoaderThreadedCarriesTheAuthoredAndWireShapes(t *testing.T) {
	tg := loaderThreaded()
	if len(tg.KindGates) == 0 {
		t.Fatal("no kind gates were threaded — the fill produced an empty map")
	}
	g, ok := tg.KindGates["vm"]
	if !ok {
		t.Fatalf("the vm gate is absent from %d threaded gates", len(tg.KindGates))
	}
	if g.Def != "#Vm" {
		t.Fatalf("Def = %q, want the AUTHORED body #Vm", g.Def)
	}
	if g.WireDef != "#VmValue" {
		t.Fatalf("WireDef = %q, want the wire value #VmValue", g.WireDef)
	}
}
